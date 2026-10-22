package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"unicode"
)

// The admin panel as an installed web app: the manifest a browser installs it by. It lives
// under the secret admin path, and every URL in it is relative to it, so the installed app
// opens there and the path never leaves the panel.

// AppNameMax is the longest name the admin may give the installed panel: launchers cut it
// much sooner, and a name is for one glance.
const AppNameMax = 30

// AppName is the installed panel's name: the admin's own (stored), else the brand, else
// "mikan" ("VPN" is the brand of a panel that never set one).
func AppName(stored, brand string) string {
	if s := cleanAppName(stored); s != "" {
		return s
	}
	if b := cleanAppName(brand); b != "" && b != "VPN" {
		return b
	}
	return "mikan"
}

func cleanAppName(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
	if rs := []rune(s); len(rs) > AppNameMax {
		s = strings.TrimSpace(string(rs[:AppNameMax]))
	}
	return s
}

// ManifestInfo is what the manifest takes from the settings.
type ManifestInfo struct {
	Name string // AppName
	Lang string // the panel's default language, "" for the visitor's
}

type manifestIcon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes"`
	Type    string `json:"type"`
	Purpose string `json:"purpose,omitempty"`
}

type manifestShortcut struct {
	Name  string         `json:"name"`
	URL   string         `json:"url"`
	Icons []manifestIcon `json:"icons"`
}

type manifest struct {
	Name            string             `json:"name"`
	ShortName       string             `json:"short_name"`
	Description     string             `json:"description"`
	Lang            string             `json:"lang"`
	Dir             string             `json:"dir"`
	StartURL        string             `json:"start_url"`
	Scope           string             `json:"scope"`
	Display         string             `json:"display"`
	BackgroundColor string             `json:"background_color"`
	ThemeColor      string             `json:"theme_color"`
	Icons           []manifestIcon     `json:"icons"`
	Shortcuts       []manifestShortcut `json:"shortcuts"`
}

// The panel's light theme (web/index.html's theme-color): the splash and the title bar
// until the panel sets the colour of the theme chosen.
const manifestColor = "#FBF1E8"

var manifestIcons = []manifestIcon{
	{Src: "pwa-192.png", Sizes: "192x192", Type: "image/png", Purpose: "any"},
	{Src: "pwa-512.png", Sizes: "512x512", Type: "image/png", Purpose: "any"},
	{Src: "pwa-maskable-512.png", Sizes: "512x512", Type: "image/png", Purpose: "maskable"},
}

// manifestShortcuts are the pages a long press on the app's icon (a right click on the
// taskbar) opens: the panel's routes, relative to the manifest.
var manifestShortcuts = []struct{ path, ru, en string }{
	{"users", "Пользователи", "Users"},
	{"nodes", "Ноды", "Nodes"},
	{"payments", "Платежи", "Payments"},
	{"settings", "Настройки", "Settings"},
}

// Manifest serves manifest.webmanifest for the admin panel; info reads the settings.
func Manifest(info func(ctx context.Context) (ManifestInfo, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		in, err := info(r.Context())
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		lang := in.Lang
		if lang != "ru" && lang != "en" {
			lang = "en"
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Accept-Language"))), "ru") {
				lang = "ru"
			}
		}
		m := manifest{
			Name: in.Name, ShortName: in.Name,
			Description: "mikan admin panel",
			Lang:        lang, Dir: "ltr",
			StartURL: "./", Scope: "./",
			Display:         "standalone",
			BackgroundColor: manifestColor, ThemeColor: manifestColor,
			Icons: manifestIcons,
		}
		if lang == "ru" {
			m.Description = "Панель управления mikan"
		}
		for _, s := range manifestShortcuts {
			name := s.en
			if lang == "ru" {
				name = s.ru
			}
			m.Shortcuts = append(m.Shortcuts, manifestShortcut{Name: name, URL: "./" + s.path, Icons: manifestIcons[:1]})
		}
		body, err := json.Marshal(m)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		h := w.Header()
		h.Set("Content-Type", "application/manifest+json")
		// Revalidated on every check: a new name reaches the installed app with the next one.
		h.Set("Cache-Control", "no-cache")
		h.Set("Vary", "Accept-Language")
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(body)
	})
}
