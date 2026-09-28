package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"mikan/internal/nodetls"
	"mikan/internal/panel/audit"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

// nodeCmd manages the panel's nodes from the server shell. Join keys go to stdout alone,
// so a script can pass them to the node's installer; they are shown once.
func nodeCmd(ctx context.Context, st *store.Store, dataDir string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("укажите list, add, key или set\n\n" + usage)
	}
	panelCert := func() (nodetls.Pair, error) {
		return nodetls.LoadOrCreate(filepath.Join(dataDir, "tls", "nodes"), time.Now())
	}
	switch args[0] {
	case "list":
		nodes, err := st.Q.ListNodes(ctx)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tИМЯ\tАДРЕС API\tДЛЯ КЛИЕНТОВ\tВКЛЮЧЕНА")
		for _, n := range nodes {
			addr, host := n.Address, domain.NodeHost(n)
			if addr == "" {
				addr, host = "локальная", "адрес панели"
			}
			on := "да"
			if n.Enabled == 0 {
				on = "нет"
			}
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", n.ID, n.Name, addr, host, on)
		}
		return tw.Flush()
	case "add":
		fs := flag.NewFlagSet("node add", flag.ContinueOnError)
		name := fs.String("name", "", "имя — группа в подписке, например «🇺🇸 США»")
		host := fs.String("host", "", "IP или имя сервера ноды")
		dom := fs.String("domain", "", "домен ноды для Hysteria2/TUIC (необязательно)")
		port := fs.Int("api-port", 0, "порт API ноды (по умолчанию случайный)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(*name) == "" || strings.TrimSpace(*host) == "" {
			return errors.New("нужны --name и --host")
		}
		panel, err := panelCert()
		if err != nil {
			return err
		}
		n, key, err := domain.AddNode(ctx, st, panel, domain.NodeInput{Name: *name, Host: *host, Domain: *dom, APIPort: *port}, time.Now())
		if err != nil {
			return err
		}
		_ = audit.Write(ctx, st.Q, time.Now(), audit.Entry{Action: "cli.node_create", TargetType: "node", TargetID: strconv.FormatInt(n.ID, 10),
			Details: map[string]any{"name": n.Name, "address": n.Address}})
		fmt.Fprintln(stdout, key)
		fmt.Fprintf(stderr, "Нода %d «%s» добавлена, API на %s. На сервере ноды: install.sh --node --join <ключ выше>\n", n.ID, n.Name, n.Address)
		return nil
	case "key":
		id, err := nodeID(args)
		if err != nil {
			return err
		}
		panel, err := panelCert()
		if err != nil {
			return err
		}
		key, err := domain.RekeyNode(ctx, st, panel, id, time.Now())
		switch {
		case errors.Is(err, domain.ErrUnknownNode):
			return fmt.Errorf("нет ноды %d", id)
		case errors.Is(err, domain.ErrLocalNode):
			return errors.New("своей ноде панели ключ не нужен")
		case err != nil:
			return err
		}
		_ = audit.Write(ctx, st.Q, time.Now(), audit.Entry{Action: "cli.node_rekey", TargetType: "node", TargetID: strconv.FormatInt(id, 10)})
		fmt.Fprintln(stdout, key)
		fmt.Fprintln(stderr, "Старый ключ ноды больше не действует.")
		return nil
	case "set":
		id, err := nodeID(args)
		if err != nil {
			return err
		}
		n, err := st.Q.GetNode(ctx, id)
		if err != nil {
			return fmt.Errorf("нет ноды %d", id)
		}
		fs := flag.NewFlagSet("node set", flag.ContinueOnError)
		name := fs.String("name", n.Name, "имя — группа в подписке")
		host := fs.String("host", n.PublicHost, "IP или имя сервера ноды")
		dom := fs.String("domain", n.Domain, "домен ноды")
		enabled := fs.Bool("enabled", n.Enabled != 0, "обслуживает ли нода клиентов")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if n.Address == "" && (*host != n.PublicHost || *dom != n.Domain) {
			return errors.New("адрес своей ноды — это адрес панели: mikan settings")
		}
		if n.Address != "" && *host != n.PublicHost {
			_, port, err := net.SplitHostPort(n.Address)
			if err != nil {
				return err
			}
			n.Address = net.JoinHostPort(strings.TrimSpace(*host), port)
		}
		on := int64(0)
		if *enabled {
			on = 1
		}
		n, err = st.Q.UpdateNode(ctx, db.UpdateNodeParams{Name: strings.TrimSpace(*name), Address: n.Address, PublicHost: strings.TrimSpace(*host),
			Domain: strings.TrimSpace(*dom), Enabled: on, UpdatedAt: time.Now().Unix(), ID: n.ID})
		if err != nil {
			return err
		}
		_ = audit.Write(ctx, st.Q, time.Now(), audit.Entry{Action: "cli.node_update", TargetType: "node", TargetID: strconv.FormatInt(id, 10),
			Details: map[string]any{"name": n.Name, "enabled": n.Enabled != 0}})
		fmt.Fprintf(stderr, "Нода %d: «%s». Панель применит изменения в течение 30 секунд.\n", n.ID, n.Name)
		return nil
	default:
		return fmt.Errorf("неизвестная подкоманда node %q\n\n%s", args[0], usage)
	}
}

func nodeID(args []string) (int64, error) {
	if len(args) < 2 {
		return 0, errors.New("укажите номер ноды: mikan admin node list")
	}
	id, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("неверный номер ноды %q", args[1])
	}
	return id, nil
}
