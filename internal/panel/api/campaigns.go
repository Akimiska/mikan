package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/store/db"
)

// Ad campaigns: links to the bot with a code, and who came, bought and renewed by them.

type CampaignView struct {
	ID        int64     `json:"id"`
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	Archived  bool      `json:"archived" doc:"В архиве: ссылка больше не метит новых людей, статистика остаётся"`
	CreatedAt time.Time `json:"created_at"`
	Link      string    `json:"link" doc:"Ссылка на бота с меткой; пусто, пока бот не запущен"`
	Came      int64     `json:"came" doc:"Сколько человек пришло по ссылке за период"`
	Bought    int64     `json:"bought" doc:"Сколько из них купили тариф или платный пробный период"`
	Renewed   int64     `json:"renewed" doc:"Сколько из них продлили"`
	// Revenue is what they paid over the period: Stars and kopecks.
	RevenueStars int64 `json:"revenue_stars"`
	RevenueRub   int64 `json:"revenue_rub" doc:"Копейки"`
}

type campaignsInput struct {
	Days int `query:"days" minimum:"0" maximum:"3650" doc:"За сколько дней статистика; 0 — за всё время"`
}

type campaignsOutput struct{ Body []CampaignView }
type campaignOutput struct{ Body CampaignView }

type createCampaignInput struct {
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"80"`
		Code string `json:"code,omitempty" maxLength:"32" doc:"Метка в ссылке: латиница, цифры, _ и -; пусто — случайная"`
	}
}

type patchCampaignInput struct {
	ID   int64 `path:"id" minimum:"1"`
	Body struct {
		Name     *string `json:"name,omitempty" minLength:"1" maxLength:"80"`
		Archived *bool   `json:"archived,omitempty"`
	}
}

type campaignIDInput struct {
	ID int64 `path:"id" minimum:"1"`
}

func (h *handlers) registerCampaigns() {
	tags := []string{"campaigns"}
	huma.Register(h.api, huma.Operation{OperationID: "list-campaigns", Method: http.MethodGet, Path: "/api/v1/campaigns", Summary: "Рекламные кампании и их статистика", Tags: tags}, h.listCampaigns)
	huma.Register(h.api, huma.Operation{OperationID: "create-campaign", Method: http.MethodPost, Path: "/api/v1/campaigns", Summary: "Создать кампанию: ссылку на бота с меткой", Tags: tags, DefaultStatus: http.StatusCreated}, h.createCampaign)
	huma.Register(h.api, huma.Operation{OperationID: "update-campaign", Method: http.MethodPatch, Path: "/api/v1/campaigns/{id}", Summary: "Переименовать кампанию или убрать в архив", Tags: tags}, h.updateCampaign)
	huma.Register(h.api, huma.Operation{OperationID: "delete-campaign", Method: http.MethodDelete, Path: "/api/v1/campaigns/{id}", Summary: "Удалить кампанию: пришедшие по ней люди перестают к ней относиться", Tags: tags, DefaultStatus: http.StatusNoContent}, h.deleteCampaign)
}

// campaignLink is the link to the bot with the campaign's code; "" while the bot is off.
func (h *handlers) campaignLink(ctx context.Context, code string) string {
	if h.d.Gateway == nil {
		return ""
	}
	bot := h.d.Gateway.BotURL(ctx)
	if bot == "" {
		return ""
	}
	return bot + "?start=" + domain.CampaignPrefix + code
}

func (h *handlers) viewCampaign(ctx context.Context, c db.Campaign, st *domain.CampaignStats) CampaignView {
	v := CampaignView{ID: c.ID, Code: c.Code, Name: c.Name, Archived: c.Archived != 0, CreatedAt: time.Unix(c.CreatedAt, 0).UTC(), Link: h.campaignLink(ctx, c.Code)}
	if st != nil {
		v.Came, v.Bought, v.Renewed = st.Came, st.Bought, st.Renewed
		v.RevenueStars, v.RevenueRub = st.Revenue["XTR"], st.Revenue["RUB"]
	}
	return v
}

func (h *handlers) listCampaigns(ctx context.Context, in *campaignsInput) (*campaignsOutput, error) {
	rows, err := h.d.Store.Q.ListCampaigns(ctx)
	if err != nil {
		return nil, err
	}
	var since time.Time
	if in.Days > 0 {
		since = h.d.Now().AddDate(0, 0, -in.Days)
	}
	stats, err := h.d.Campaigns.Stats(ctx, since)
	if err != nil {
		return nil, err
	}
	out := &campaignsOutput{Body: make([]CampaignView, 0, len(rows))}
	for _, c := range rows {
		out.Body = append(out.Body, h.viewCampaign(ctx, c, stats[c.ID]))
	}
	return out, nil
}

func (h *handlers) createCampaign(ctx context.Context, in *createCampaignInput) (*campaignOutput, error) {
	c, err := h.d.Campaigns.Create(ctx, in.Body.Name, in.Body.Code)
	switch {
	case errors.Is(err, domain.ErrCampaignCode):
		return nil, fieldErr("body.code", "campaign_code")
	case errors.Is(err, domain.ErrCampaignTaken):
		return nil, fieldErr("body.code", "campaign_taken")
	case err != nil:
		return nil, err
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "campaign.create", "campaign", strconv.FormatInt(c.ID, 10), map[string]any{"code": c.Code})
	return &campaignOutput{Body: h.viewCampaign(ctx, c, nil)}, nil
}

func (h *handlers) updateCampaign(ctx context.Context, in *patchCampaignInput) (*campaignOutput, error) {
	var cur db.Campaign
	rows, err := h.d.Store.Q.ListCampaigns(ctx)
	if err != nil {
		return nil, err
	}
	found := false
	for _, c := range rows {
		if c.ID == in.ID {
			cur, found = c, true
		}
	}
	if !found {
		return nil, huma.Error404NotFound("not_found")
	}
	if in.Body.Name != nil {
		cur.Name = strings.TrimSpace(*in.Body.Name)
	}
	if in.Body.Archived != nil {
		cur.Archived = domain.Flag(*in.Body.Archived)
	}
	c, err := h.d.Store.Q.UpdateCampaign(ctx, db.UpdateCampaignParams{Name: cur.Name, Archived: cur.Archived, ID: cur.ID})
	if err != nil {
		return nil, err
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "campaign.update", "campaign", strconv.FormatInt(c.ID, 10), map[string]any{"archived": c.Archived != 0})
	return &campaignOutput{Body: h.viewCampaign(ctx, c, nil)}, nil
}

func (h *handlers) deleteCampaign(ctx context.Context, in *campaignIDInput) (*struct{}, error) {
	n, err := h.d.Store.Q.DeleteCampaign(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, huma.Error404NotFound("not_found")
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "campaign.delete", "campaign", strconv.FormatInt(in.ID, 10), nil)
	return nil, nil
}
