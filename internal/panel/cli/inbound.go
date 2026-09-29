package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"mikan/internal/panel/audit"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/presets"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

// inboundCmd lists, adds and moves inbounds from the server shell. The running panel pushes
// the change to the node on its next reconcile, within 30 seconds.
func inboundCmd(ctx context.Context, st *store.Store, set *settings.Settings, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("укажите list, add или set\n\n" + usage)
	}
	switch args[0] {
	case "list":
		inbounds, err := st.Q.ListInbounds(ctx)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "НОДА\tИМЯ\tПРЕСЕТ\tПОРТ\tВКЛЮЧЕНО")
		for _, in := range inbounds {
			on := "да"
			if in.Enabled == 0 {
				on = "нет"
			}
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s/%s\t%s\n", in.NodeID, in.Name, in.Preset, in.Port, domain.InboundNetwork(in), on)
		}
		return tw.Flush()
	case "add":
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			return errors.New("укажите пресет: mikan admin inbound add ПРЕСЕТ [--port ПОРТ] [--node НОДА]\nПресеты: " + presetIDs())
		}
		fs := flag.NewFlagSet("inbound add", flag.ContinueOnError)
		port := fs.String("port", "", "порт или диапазон (по умолчанию — порт пресета)")
		node := fs.Int64("node", 1, "нода (1 — своя нода панели, см. mikan admin node list)")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		row, err := domain.AddPreset(ctx, st, set, *node, args[1], *port, time.Now())
		var busy *domain.PortInUseError
		switch {
		case errors.Is(err, domain.ErrUnknownNode):
			return fmt.Errorf("нет ноды %d", *node)
		case errors.Is(err, domain.ErrUnknownPreset):
			return fmt.Errorf("нет пресета %q. Пресеты: %s", args[1], presetIDs())
		case errors.Is(err, domain.ErrBadPort):
			return fmt.Errorf("неверный порт %q", *port)
		case errors.As(err, &busy):
			return fmt.Errorf("порт уже занят подключением %s, укажите другой: --port", busy.Owner)
		case err != nil:
			return err
		}
		_ = audit.Write(ctx, st.Q, time.Now(), audit.Entry{Action: "cli.inbound_create", TargetType: "inbound", TargetID: row.Name,
			Details: map[string]any{"preset": row.Preset, "port": row.Port}})
		fmt.Fprintf(stderr, "Добавлено подключение %s на %s/%s. Нода получит его в течение 30 секунд.\n", row.Name, row.Port, domain.InboundNetwork(row))
		return openPort(ctx, st, row, stdout, stderr)
	case "set":
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			return errors.New("укажите подключение: mikan admin inbound set ИМЯ --port ПОРТ [--node НОДА]\nИмена: mikan admin inbound list")
		}
		fs := flag.NewFlagSet("inbound set", flag.ContinueOnError)
		port := fs.String("port", "", "новый порт или диапазон")
		node := fs.Int64("node", 1, "нода подключения (см. mikan admin inbound list)")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if *port == "" {
			return errors.New("укажите новый порт: --port")
		}
		prev, row, err := domain.SetInboundPort(ctx, st, *node, args[1], *port, time.Now())
		var busy *domain.PortInUseError
		switch {
		case errors.Is(err, domain.ErrUnknownNode):
			return fmt.Errorf("нет ноды %d", *node)
		case errors.Is(err, domain.ErrUnknownInbound):
			return fmt.Errorf("на ноде %d нет подключения %q: mikan admin inbound list", *node, args[1])
		case errors.Is(err, domain.ErrBadPort):
			return fmt.Errorf("неверный порт %q", *port)
		case errors.As(err, &busy):
			return fmt.Errorf("порт уже занят подключением %s, укажите другой", busy.Owner)
		case err != nil:
			return err
		}
		_ = audit.Write(ctx, st.Q, time.Now(), audit.Entry{Action: "cli.inbound_update", TargetType: "inbound", TargetID: row.Name,
			Details: map[string]any{"node": row.NodeID, "port": row.Port, "old_port": prev.Port}})
		fmt.Fprintf(stderr, "Подключение %s: порт %s → %s/%s. Нода получит изменение в течение 30 секунд, клиентам нужно обновить подписку.\n",
			row.Name, prev.Port, row.Port, domain.InboundNetwork(row))
		return openPort(ctx, st, row, stdout, stderr)
	default:
		return fmt.Errorf("неизвестная подкоманда inbound %q\n\n%s", args[0], usage)
	}
}

// openPort prints "port/network" on stdout for the server script, which opens it in ufw.
// A remote node's port is opened on that node's server, so only a hint goes out then.
func openPort(ctx context.Context, st *store.Store, in db.Inbound, stdout, stderr io.Writer) error {
	n, err := st.Q.GetNode(ctx, in.NodeID)
	if err != nil {
		return err
	}
	rule := in.Port + "/" + domain.InboundNetwork(in)
	if n.Address != "" {
		fmt.Fprintf(stderr, "Откройте порт на сервере ноды %s: ufw allow %s\n", n.PublicHost, strings.Replace(rule, "-", ":", 1))
		return nil
	}
	fmt.Fprintln(stdout, rule)
	return nil
}

func presetIDs() string {
	var ids []string
	for _, p := range presets.All {
		if p.ID != presets.Custom {
			ids = append(ids, p.ID)
		}
	}
	return strings.Join(ids, ", ")
}
