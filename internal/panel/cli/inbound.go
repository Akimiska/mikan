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
)

// inboundCmd lists and adds inbounds from the server shell. The running panel pushes the
// change to the node on its next reconcile, within 30 seconds.
func inboundCmd(ctx context.Context, st *store.Store, set *settings.Settings, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("укажите list или add\n\n" + usage)
	}
	switch args[0] {
	case "list":
		inbounds, err := st.Q.ListInbounds(ctx)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ИМЯ\tПРЕСЕТ\tПОРТ\tВКЛЮЧЕНО")
		for _, in := range inbounds {
			on := "да"
			if in.Enabled == 0 {
				on = "нет"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s/%s\t%s\n", in.Name, in.Preset, in.Port, domain.InboundNetwork(in), on)
		}
		return tw.Flush()
	case "add":
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			return errors.New("укажите пресет: mikan admin inbound add ПРЕСЕТ [--port ПОРТ]\nПресеты: " + presetIDs())
		}
		fs := flag.NewFlagSet("inbound add", flag.ContinueOnError)
		port := fs.String("port", "", "порт или диапазон (по умолчанию — порт пресета)")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		row, err := domain.AddPreset(ctx, st, set, args[1], *port, time.Now())
		var busy *domain.PortInUseError
		switch {
		case errors.Is(err, domain.ErrUnknownPreset):
			return fmt.Errorf("нет пресета %q. Пресеты: %s", args[1], presetIDs())
		case errors.Is(err, domain.ErrBadPort):
			return fmt.Errorf("неверный порт %q", *port)
		case errors.As(err, &busy):
			return fmt.Errorf("порт уже занят подключением %s, укажите другой: --port", busy.Owner)
		case err != nil:
			return err
		}
		network := domain.InboundNetwork(row)
		_ = audit.Write(ctx, st.Q, time.Now(), audit.Entry{Action: "cli.inbound_create", TargetType: "inbound", TargetID: row.Name,
			Details: map[string]any{"preset": row.Preset, "port": row.Port}})
		// stdout carries only "port/network": the server script opens it in ufw.
		fmt.Fprintf(stdout, "%s/%s\n", row.Port, network)
		fmt.Fprintf(stderr, "Добавлено подключение %s на %s/%s. Нода получит его в течение 30 секунд.\n", row.Name, row.Port, network)
		return nil
	default:
		return fmt.Errorf("неизвестная подкоманда inbound %q\n\n%s", args[0], usage)
	}
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
