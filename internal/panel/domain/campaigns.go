package domain

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"

	"mikan/internal/panel/model"
	"mikan/internal/panel/secure"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

// Ad campaigns: a link to the bot with a campaign's code, and what came of it. An account
// that starts the bot by the link with no subscription yet belongs to the campaign; the
// campaign counts the accounts that came, those that bought a plan (or a priced trial),
// those that renewed, and the money they paid.

// CampaignPrefix starts the /start parameter of a campaign link: /start c_<code>.
const CampaignPrefix = model.CampaignPrefix

var (
	ErrCampaignCode  = errors.New("campaign_code")  // not a code: a-z, 0-9, _ and -, up to 32
	ErrCampaignTaken = errors.New("campaign_taken") // another campaign has the code
)

var campaignCode = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

// CampaignStats is what came of a campaign over a period.
type CampaignStats struct {
	Came    int64            // accounts that started the bot by the link
	Bought  int64            // of them, accounts that bought a plan or a priced trial
	Renewed int64            // of them, accounts that renewed
	Revenue map[string]int64 // paid, by currency (XTR, RUB in kopecks)
}

type Campaigns struct {
	st  *store.Store
	now func() time.Time
}

func NewCampaigns(st *store.Store, now func() time.Time) *Campaigns {
	return &Campaigns{st: st, now: now}
}

// Create adds a campaign; an empty code gets a random one.
func (c *Campaigns) Create(ctx context.Context, name, code string) (db.Campaign, error) {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" {
		code = strings.ToLower(secure.Token(8))
	}
	if !campaignCode.MatchString(code) {
		return db.Campaign{}, ErrCampaignCode
	}
	row, err := c.st.Q.CreateCampaign(ctx, db.CreateCampaignParams{Code: code, Name: strings.TrimSpace(name), CreatedAt: c.now().Unix()})
	if store.IsUnique(err) {
		return row, ErrCampaignTaken
	}
	return row, err
}

// Tag makes tgID the campaign's when it comes by the link and has no subscription yet; an
// unknown or archived code tags nothing. It reports whether the account was tagged.
func (c *Campaigns) Tag(ctx context.Context, tgID int64, code string) (bool, error) {
	code = strings.ToLower(strings.TrimSpace(code))
	if !campaignCode.MatchString(code) {
		return false, nil
	}
	camp, err := c.st.Q.GetCampaignByCode(ctx, code)
	if errors.Is(err, sql.ErrNoRows) || err == nil && camp.Archived != 0 {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	n, err := c.st.Q.TagCampaign(ctx, db.TagCampaignParams{TgID: tgID, Now: c.now().Unix(), CampaignID: sql.NullInt64{Int64: camp.ID, Valid: true}})
	return n > 0, err
}

// Stats is what came of every campaign since since (zero: from the start), by campaign id.
func (c *Campaigns) Stats(ctx context.Context, since time.Time) (map[int64]*CampaignStats, error) {
	from := int64(0)
	if !since.IsZero() {
		from = since.Unix()
	}
	out := map[int64]*CampaignStats{}
	of := func(id int64) *CampaignStats {
		s := out[id]
		if s == nil {
			s = &CampaignStats{Revenue: map[string]int64{}}
			out[id] = s
		}
		return s
	}
	came, err := c.st.Q.CampaignArrivals(ctx, sql.NullInt64{Int64: from, Valid: true})
	if err != nil {
		return nil, err
	}
	for _, r := range came {
		of(r.CampaignID.Int64).Came = r.Came
	}
	pays, err := c.st.Q.CampaignPayments(ctx, sql.NullInt64{Int64: from, Valid: true})
	if err != nil {
		return nil, err
	}
	type acct struct{ campaign, tg int64 }
	bought, renewed := map[acct]bool{}, map[acct]bool{}
	for _, p := range pays {
		s := of(p.CampaignID.Int64)
		s.Revenue[p.Currency] += p.Amount
		a := acct{p.CampaignID.Int64, p.TgID}
		switch p.Kind {
		case "new", "trial":
			if !bought[a] {
				bought[a] = true
				s.Bought++
			}
		case "renew":
			if !renewed[a] {
				renewed[a] = true
				s.Renewed++
			}
		}
	}
	return out, nil
}
