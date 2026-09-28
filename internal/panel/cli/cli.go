package cli

import (
	"bufio"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"mikan/internal/panel/api"
	"mikan/internal/panel/app"
	"mikan/internal/panel/audit"
	"mikan/internal/panel/auth"
	"mikan/internal/panel/config"
	"mikan/internal/panel/secure"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

const usage = `mikan — панель управления VPN на ядре mihomo

Команды:
  serve                         запустить панель
  admin bootstrap [флаги]       первичная настройка: админ, пути, адрес
  admin url                     показать ссылку на панель
  admin reset-password          сменить пароль админа (все сессии завершаются)
  admin reset-path              выдать новую секретную ссылку на панель
  admin disable-2fa             выключить 2FA у админа
  admin backup ФАЙЛ             сделать консистентную копию базы на ходу
  admin inbound list            подключения: имя, пресет, порт
  admin inbound add ПРЕСЕТ [--port ПОРТ]
                                добавить подключение из пресета со свежими ключами (--node НОДА)
  admin node list               ноды панели
  admin node add --name ИМЯ --host IP [--domain Д] [--api-port П]
                                добавить ноду; печатает ключ для install.sh --node --join
  admin node key НОДА           новый ключ ноды (старый перестаёт работать)
  admin node set НОДА [--name] [--host] [--domain] [--enabled]
  health                        проверить, что панель отвечает (healthcheck контейнера)
  openapi                       вывести OpenAPI-спецификацию (для генерации клиента)
  version                       версия
`

func Run(ctx context.Context, args []string, version string, web fs.FS) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	switch args[0] {
	case "serve":
		cfg, err := config.FromEnv()
		if err != nil {
			return err
		}
		return app.Serve(ctx, cfg, version, web)
	case "admin":
		return adminCmd(ctx, args[1:])
	case "openapi":
		_, humaAPI, err := api.New(api.Deps{Version: version, Now: time.Now})
		if err != nil {
			return err
		}
		out, err := json.MarshalIndent(humaAPI.OpenAPI(), "", "  ")
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(append(out, '\n'))
		return err
	case "version":
		fmt.Println(version)
		return nil
	case "health":
		return health()
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		return fmt.Errorf("неизвестная команда %q\n\n%s", args[0], usage)
	}
}

func adminCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("укажите подкоманду admin\n\n" + usage)
	}
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, cfg.DataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	set := settings.New(st.Q)
	switch args[0] {
	case "bootstrap":
		return bootstrap(ctx, st, set, args[1:])
	case "url":
		u, err := panelURL(ctx, set)
		if err != nil {
			return err
		}
		login := ""
		if a, err := findAdmin(ctx, st, ""); err == nil {
			login = a.Username
		}
		printURL(os.Stdout, os.Stderr, u, login)
		return nil
	case "reset-password":
		return resetPassword(ctx, st, args[1:])
	case "reset-path":
		if err := settings.Set(ctx, set, settings.KeyAdminPath, secure.Token(24)); err != nil {
			return err
		}
		_ = audit.Write(ctx, st.Q, time.Now(), audit.Entry{Action: "cli.reset_path"})
		u, err := panelURL(ctx, set)
		if err != nil {
			return err
		}
		fmt.Println("Новая ссылка (старая перестанет работать в течение 5 секунд):")
		fmt.Println(u)
		return nil
	case "backup":
		if len(args) < 2 {
			return errors.New("укажите файл: mikan admin backup /data/backup.db")
		}
		// VACUUM INTO writes a consistent copy while the panel keeps running.
		if _, err := st.DB.ExecContext(ctx, "VACUUM INTO ?", args[1]); err != nil {
			return fmt.Errorf("backup: %w", err)
		}
		if err := os.Chmod(args[1], 0o600); err != nil {
			return err
		}
		fmt.Println("Копия базы:", args[1])
		return nil
	case "node":
		return nodeCmd(ctx, st, cfg.DataDir, args[1:], os.Stdout, os.Stderr)
	case "inbound":
		return inboundCmd(ctx, st, set, args[1:], os.Stdout, os.Stderr)
	case "disable-2fa":
		fs := flag.NewFlagSet("disable-2fa", flag.ContinueOnError)
		username := fs.String("username", "", "логин админа (можно не указывать, если админ один)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		a, err := findAdmin(ctx, st, *username)
		if err != nil {
			return err
		}
		if err := st.Q.SetAdminTOTP(ctx, db.SetAdminTOTPParams{ID: a.ID}); err != nil {
			return err
		}
		_ = audit.Write(ctx, st.Q, time.Now(), audit.Entry{Action: "cli.disable_2fa", TargetType: "admin", TargetID: a.Username})
		fmt.Printf("2FA для %s выключена.\n", a.Username)
		return nil
	default:
		return fmt.Errorf("неизвестная подкоманда admin %q\n\n%s", args[0], usage)
	}
}

func bootstrap(ctx context.Context, st *store.Store, set *settings.Settings, args []string) error {
	fs := flag.NewFlagSet("bootstrap", flag.ContinueOnError)
	username := fs.String("username", "", "логин админа (по умолчанию — случайный)")
	host := fs.String("public-host", "", "публичный IP или домен сервера (обязательно)")
	port := fs.Int("port", 0, "порт панели (обязательно)")
	domain := fs.String("domain", "", "домен для сертификата Let's Encrypt (необязательно)")
	email := fs.String("email", "", "email для Let's Encrypt (необязательно)")
	adminPath := fs.String("admin-path", "", "секретный путь панели (по умолчанию — случайный)")
	subPath := fs.String("sub-path", "", "путь подписок (по умолчанию — случайный)")
	passwordStdin := fs.Bool("password-stdin", false, "прочитать пароль из stdin вместо генерации")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *host == "" || *port <= 0 || *port > 65535 {
		return errors.New("нужны --public-host и --port")
	}
	if n, err := st.Q.CountAdmins(ctx); err != nil {
		return err
	} else if n > 0 {
		return errors.New("панель уже инициализирована; для нового пароля — `mikan admin reset-password`")
	}
	password, generated, err := readOrGeneratePassword(*passwordStdin, os.Stdin)
	if err != nil {
		return err
	}
	if *adminPath == "" {
		*adminPath = secure.Token(24)
	}
	if *subPath == "" {
		*subPath = secure.Token(12)
	}
	if err := validPathSegment(*adminPath, 16); err != nil {
		return fmt.Errorf("--admin-path: %w", err)
	}
	if err := validPathSegment(*subPath, 8); err != nil {
		return fmt.Errorf("--sub-path: %w", err)
	}
	name := strings.ToLower(strings.TrimSpace(*username))
	if name == "" {
		name = secure.Login()
	}
	if err := validLogin(name); err != nil {
		return fmt.Errorf("--username: %w", err)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	err = st.Tx(ctx, func(q *db.Queries) error {
		txSet := settings.New(q)
		if _, err := q.CreateAdmin(ctx, db.CreateAdminParams{Username: name, PasswordHash: hash, CreatedAt: time.Now().Unix()}); err != nil {
			return err
		}
		for k, v := range map[string]any{
			settings.KeyAdminPath: *adminPath, settings.KeySubPath: *subPath,
			settings.KeyPublicHost: *host, settings.KeyPanelPort: *port,
			settings.KeyDomain: *domain, settings.KeyACMEEmail: *email,
		} {
			if err := settings.Set(ctx, txSet, k, v); err != nil {
				return err
			}
		}
		return audit.Write(ctx, q, time.Now(), audit.Entry{Action: "cli.bootstrap", TargetType: "admin", TargetID: name})
	})
	if err != nil {
		return err
	}
	u, err := panelURL(ctx, set)
	if err != nil {
		return err
	}
	fmt.Println("Панель готова.")
	fmt.Println("  Адрес:  " + u)
	fmt.Println("  Логин:  " + name)
	if generated {
		fmt.Println("  Пароль: " + password + "   ← показывается один раз, сохраните его")
	}
	return nil
}

func resetPassword(ctx context.Context, st *store.Store, args []string) error {
	fs := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	username := fs.String("username", "", "логин админа (можно не указывать, если админ один)")
	passwordStdin := fs.Bool("password-stdin", false, "прочитать пароль из stdin вместо генерации")
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, err := findAdmin(ctx, st, *username)
	if err != nil {
		return err
	}
	password, generated, err := readOrGeneratePassword(*passwordStdin, os.Stdin)
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	err = st.Tx(ctx, func(q *db.Queries) error {
		if err := q.SetAdminPassword(ctx, db.SetAdminPasswordParams{PasswordHash: hash, ID: a.ID}); err != nil {
			return err
		}
		if err := q.DeleteAdminSessions(ctx, a.ID); err != nil {
			return err
		}
		return audit.Write(ctx, q, time.Now(), audit.Entry{Action: "cli.reset_password", TargetType: "admin", TargetID: a.Username})
	})
	if err != nil {
		return err
	}
	fmt.Printf("Пароль для %s изменён, все сессии завершены.\n", a.Username)
	if generated {
		fmt.Println("Новый пароль: " + password + "   ← показывается один раз")
	}
	return nil
}

// health is the container healthcheck: the panel must answer "/" with its bare 404.
func health() error {
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	scheme := "https"
	if cfg.Dev {
		scheme = "http"
	}
	// Liveness of the local listener only; the certificate is not what is being checked.
	c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	resp, err := c.Get(scheme + "://" + net.JoinHostPort(host, port) + "/")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	fmt.Println("ok")
	return nil
}

func readOrGeneratePassword(fromStdin bool, r io.Reader) (string, bool, error) {
	if !fromStdin {
		return secure.Token(32), true, nil
	}
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", false, err
	}
	pw := strings.TrimRight(line, "\r\n")
	if len(pw) < 12 {
		return "", false, errors.New("пароль должен быть не короче 12 символов")
	}
	return pw, false, nil
}

// printURL: stdout carries only the link, because the host `mikan` script of every
// installed version parses it (`mikan update` waits for the panel with it); the login goes
// to stderr, which still shows in a terminal.
func printURL(stdout, stderr io.Writer, url, login string) {
	fmt.Fprintln(stdout, url)
	if login != "" {
		fmt.Fprintln(stderr, "Логин: "+login)
	}
}

// findAdmin resolves --username; an empty name means "the only admin", since the login
// is random since 0.1.2 and nobody should have to look it up to reset a password.
func findAdmin(ctx context.Context, st *store.Store, username string) (db.Admin, error) {
	if username != "" {
		a, err := st.Q.GetAdminByUsername(ctx, strings.ToLower(strings.TrimSpace(username)))
		if errors.Is(err, sql.ErrNoRows) {
			return a, fmt.Errorf("админ %q не найден", username)
		}
		return a, err
	}
	admins, err := st.Q.ListAdmins(ctx)
	if err != nil {
		return db.Admin{}, err
	}
	switch len(admins) {
	case 0:
		return db.Admin{}, errors.New("админов нет: панель не инициализирована")
	case 1:
		return admins[0], nil
	}
	return db.Admin{}, errors.New("админов несколько, укажите --username")
}

func validLogin(s string) error {
	if len(s) < 3 || len(s) > 32 {
		return errors.New("длина логина — от 3 до 32 символов")
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return errors.New("логин — только a-z, 0-9, точка, дефис и подчёркивание")
		}
	}
	return nil
}

func validPathSegment(s string, minLen int) error {
	if len(s) < minLen || len(s) > 64 {
		return fmt.Errorf("длина должна быть от %d до 64 символов", minLen)
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return errors.New("допустимы только латинские буквы, цифры, - и _")
		}
	}
	return nil
}

func panelURL(ctx context.Context, set *settings.Settings) (string, error) {
	ep, err := set.Endpoint(ctx)
	if err != nil {
		return "", err
	}
	p, err := set.Paths(ctx)
	if err != nil {
		return "", err
	}
	if ep.Host == "" || p.Admin == "" {
		return "", errors.New("панель не инициализирована: выполните `mikan admin bootstrap`")
	}
	return "https://" + net.JoinHostPort(ep.Host, strconv.Itoa(ep.Port)) + "/" + p.Admin + "/", nil
}
