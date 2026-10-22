package api

import (
	"context"
	"database/sql"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// Load balancing between nodes (domain.Balancer).

type BalanceGroupView struct {
	Group string            `json:"group"`
	Nodes []BalanceNodeView `json:"nodes"`
}

type BalanceNodeView struct {
	NodeID   int64  `json:"node_id"`
	Clients  int64  `json:"clients" doc:"Сколько действующих клиентов закреплено за нодой"`
	Capacity *int64 `json:"capacity" doc:"Мест для клиентов; null — без ограничения"`
	Full     bool   `json:"full" doc:"Мест не осталось: новые клиенты группы идут на наименее загруженную ноду"`
}

type balanceOutput struct{ Body []BalanceGroupView }

type setNodeBalanceInput struct {
	ID   int64 `path:"id" minimum:"1"`
	Body struct {
		Group    string `json:"group" maxLength:"40" doc:"Группа балансировки; пусто — нода вне групп, её получают все"`
		Capacity *int64 `json:"capacity,omitempty" minimum:"1" maximum:"1000000" doc:"Мест для клиентов; не передано — без ограничения"`
	}
}

type redistributeInput struct {
	Group string `path:"group" maxLength:"40"`
}

type redistributeOutput struct {
	Body struct {
		Moved int64 `json:"moved" doc:"Сколько клиентов распределится заново при следующем обновлении подписки"`
	}
}

var balanceGroupName = regexp.MustCompile(`^[\p{L}\p{N} _.-]{1,40}$`)

func (h *handlers) registerBalance() {
	tags := []string{"node"}
	huma.Register(h.api, huma.Operation{OperationID: "list-balance", Method: http.MethodGet, Path: "/api/v1/balance", Summary: "Группы балансировки и загрузка их нод", Tags: tags}, h.listBalance)
	huma.Register(h.api, huma.Operation{OperationID: "set-node-balance", Method: http.MethodPut, Path: "/api/v1/nodes/{id}/balance", Summary: "Группа балансировки ноды и сколько клиентов она принимает", Tags: tags}, h.setNodeBalance)
	huma.Register(h.api, huma.Operation{OperationID: "redistribute-balance", Method: http.MethodPost, Path: "/api/v1/balance/{group}/redistribute",
		Summary: "Распределить клиентов группы заново: каждый попадёт на самую свободную ноду при следующем обновлении подписки", Tags: tags}, h.redistributeBalance)
}

func (h *handlers) listBalance(ctx context.Context, _ *struct{}) (*balanceOutput, error) {
	groups, err := h.d.Balancer.Loads(ctx)
	if err != nil {
		return nil, err
	}
	out := &balanceOutput{Body: make([]BalanceGroupView, 0, len(groups))}
	for _, g := range groups {
		v := BalanceGroupView{Group: g.Group, Nodes: make([]BalanceNodeView, 0, len(g.Nodes))}
		for _, n := range g.Nodes {
			nv := BalanceNodeView{NodeID: n.NodeID, Clients: n.Clients, Full: n.Full()}
			if n.Capacity.Valid {
				c := n.Capacity.Int64
				nv.Capacity = &c
			}
			v.Nodes = append(v.Nodes, nv)
		}
		out.Body = append(out.Body, v)
	}
	return out, nil
}

func (h *handlers) setNodeBalance(ctx context.Context, in *setNodeBalanceInput) (*nodeInfoOutput, error) {
	group := strings.TrimSpace(in.Body.Group)
	if group != "" && !balanceGroupName.MatchString(group) {
		return nil, fieldErr("body.group", "balance_group")
	}
	if _, err := h.getNode(ctx, in.ID); err != nil {
		return nil, err
	}
	capacity := sql.NullInt64{}
	if in.Body.Capacity != nil {
		capacity = sql.NullInt64{Int64: *in.Body.Capacity, Valid: true}
	}
	if err := h.d.Balancer.SetNode(ctx, in.ID, group, capacity); err != nil {
		return nil, err
	}
	h.d.Changes.SlotsChanged()
	h.audit(ctx, sessionOf(ctx).AdminID, "node.balance", "node", strconv.FormatInt(in.ID, 10), map[string]any{"group": group, "capacity": capacity.Int64})
	return h.nodeInfo(ctx, in.ID)
}

func (h *handlers) redistributeBalance(ctx context.Context, in *redistributeInput) (*redistributeOutput, error) {
	n, err := h.d.Balancer.Redistribute(ctx, in.Group)
	if err != nil {
		return nil, err
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "balance.redistribute", "", in.Group, map[string]any{"moved": n})
	out := &redistributeOutput{}
	out.Body.Moved = n
	return out, nil
}
