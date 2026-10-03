package panelimport

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/store"
)

// JobTimeout bounds a whole import: reading the old panel and making the users.
const JobTimeout = time.Hour

// ErrBusy: an import is running already.
var ErrBusy = errors.New("import_busy")

// JobState is where the import is.
type JobState struct {
	State    string     `json:"state" enum:"idle,fetching,importing,done,failed" doc:"idle — ещё не было; fetching — читается старая панель; importing — создаются пользователи"`
	From     string     `json:"from,omitempty" doc:"Панель и её адрес (без пути и данных для входа)"`
	Total    int        `json:"total"`
	Done     int        `json:"done"`
	Report   *Report    `json:"report,omitempty"`
	Error    string     `json:"error,omitempty" doc:"Код ошибки, когда state = failed"`
	Started  *time.Time `json:"started,omitempty"`
	Finished *time.Time `json:"finished,omitempty"`
}

// Importer runs one import at a time, apart from the request that asked for it: a closed
// tab or a proxy timeout does not stop it in the middle, and its report waits in State.
type Importer struct {
	st    *store.Store
	users *domain.Users
	hc    *http.Client
	now   func() time.Time
	log   *slog.Logger
	// Done, when not nil, runs after an import that made users (it files the old links'
	// kind in the settings).
	Done func(ctx context.Context, kind Kind)

	mu    sync.Mutex
	state JobState
}

func NewImporter(st *store.Store, users *domain.Users, hc *http.Client, now func() time.Time, log *slog.Logger) *Importer {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if hc == nil {
		hc = Client()
	}
	return &Importer{st: st, users: users, hc: hc, now: now, log: log, state: JobState{State: "idle"}}
}

// HTTP is the client the importer reads old panels with.
func (im *Importer) HTTP() *http.Client { return im.hc }

// State is a copy of where the import is.
func (im *Importer) State() JobState {
	im.mu.Lock()
	defer im.mu.Unlock()
	s := im.state
	if s.Report != nil {
		r := *s.Report
		s.Report = &r
	}
	return s
}

func (im *Importer) update(fn func(*JobState)) {
	im.mu.Lock()
	fn(&im.state)
	im.mu.Unlock()
}

// Start reads the old panel and makes its users on tariffID in the background; from names
// the source for the state ("marzban https://panel.example.com").
func (im *Importer) Start(src Source, tariffID int64, from string) error {
	im.mu.Lock()
	if im.state.State == "fetching" || im.state.State == "importing" {
		im.mu.Unlock()
		return ErrBusy
	}
	started := im.now()
	im.state = JobState{State: "fetching", From: from, Started: &started}
	im.mu.Unlock()
	go im.run(src, tariffID)
	return nil
}

func (im *Importer) run(src Source, tariffID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), JobTimeout)
	defer cancel()
	fail := func(err error) {
		im.log.Warn("import", "from", src.Kind, "err", err)
		finished := im.now()
		im.update(func(s *JobState) { s.State, s.Error, s.Finished = "failed", Code(err), &finished })
	}
	list, err := Fetch(ctx, im.hc, src)
	if err != nil {
		fail(err)
		return
	}
	im.update(func(s *JobState) { s.State, s.Total = "importing", len(list) })
	report, err := Apply(ctx, im.st, im.users, im.now(), src.Kind, tariffID, list, func(done int) {
		im.update(func(s *JobState) { s.Done = done })
	})
	if err != nil {
		fail(err)
		return
	}
	if report.Created > 0 && im.Done != nil {
		im.Done(ctx, src.Kind)
	}
	finished := im.now()
	im.update(func(s *JobState) { s.State, s.Report, s.Finished = "done", &report, &finished })
}

// Code is the error code for err.
func Code(err error) string {
	for _, known := range []error{ErrAuth, ErrUnreachable, ErrTLS, ErrRedirect, ErrAddress, ErrAnswer, ErrBusy} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return "tariff_not_found"
	case errors.Is(err, context.DeadlineExceeded):
		return "import_timeout"
	}
	return "import_failed"
}
