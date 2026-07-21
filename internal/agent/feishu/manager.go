package agentfeishu

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/agent/integration/webhook"
	"github.com/Pimeng/gooophira-mp/internal/common/agentproto"
	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/google/uuid"
	"github.com/larksuite/oapi-sdk-go/v3/scene/registration"
	"gopkg.in/yaml.v3"
)

type task struct {
	mu                             sync.RWMutex
	ID, Status, QRURL              string
	QRExpiresAt                    time.Time
	Interval                       int
	ClientID, ClientSecret, OpenID string
	Err                            string
	params                         agentproto.FeishuAppRegistrationParams
	ctx                            context.Context
	cancel                         context.CancelFunc
}
type Manager struct {
	mu         sync.RWMutex
	tasks      map[string]*task
	configPath string
	dispatcher *webhook.Dispatcher
}

func NewManager(configPath string, dispatcher *webhook.Dispatcher) *Manager {
	return &Manager{tasks: make(map[string]*task), configPath: configPath, dispatcher: dispatcher}
}
func (m *Manager) Handle(ctx context.Context, req agentproto.QueryRequest) agentproto.QueryResponse {
	var p agentproto.FeishuAppRegistrationParams
	if json.Unmarshal(req.Params, &p) != nil {
		return response(req.ID, 400, map[string]any{"ok": false, "error": "invalid-request"})
	}
	switch p.Action {
	case "start":
		return m.start(req.ID, p)
	case "status":
		return m.status(req.ID, p.TaskID)
	case "cancel":
		return m.cancel(req.ID, p.TaskID)
	default:
		return response(req.ID, 400, map[string]any{"ok": false, "error": "invalid-action"})
	}
}
func (m *Manager) start(id string, p agentproto.FeishuAppRegistrationParams) agentproto.QueryResponse {
	if strings.TrimSpace(p.TargetID) == "" {
		return response(id, 400, map[string]any{"ok": false, "error": "target_id-required"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	t := &task{ID: uuid.NewString(), Status: "pending", params: p, ctx: ctx, cancel: cancel}
	m.mu.Lock()
	m.tasks[t.ID] = t
	m.mu.Unlock()
	go m.run(t)
	return m.status(id, t.ID)
}
func (m *Manager) run(t *task) {
	t.mu.RLock()
	ctx := t.ctx
	t.mu.RUnlock()
	t.mu.Lock()
	t.Status = "waiting_qr"
	t.mu.Unlock()
	preset := false
	result, err := registration.RegisterApp(ctx, &registration.Options{CreateOnly: true, AppPreset: nil, Addons: &registration.AppAddons{Preset: &preset, Scopes: registration.AppAddonsScopes{Tenant: []string{"cardkit:card:write", "im:message:send_as_bot", "im:resource"}}}, OnQRCode: func(q *registration.QRCodeInfo) {
		t.mu.Lock()
		t.QRURL = q.URL
		t.QRExpiresAt = time.Now().Add(time.Duration(q.ExpireIn) * time.Second)
		t.Status = "qr_ready"
		t.mu.Unlock()
	}, OnStatusChange: func(s *registration.StatusChangeInfo) {
		t.mu.Lock()
		t.Status = s.Status
		t.Interval = s.Interval
		t.mu.Unlock()
	}})
	if err != nil {
		t.mu.Lock()
		if t.Status != "cancelled" {
			t.Status = "failed"
			t.Err = err.Error()
		}
		t.mu.Unlock()
		return
	}
	t.mu.Lock()
	if t.Status == "cancelled" {
		t.mu.Unlock()
		return
	}
	t.Status = "completed"
	t.ClientID = result.ClientID
	t.ClientSecret = result.ClientSecret
	if result.UserInfo != nil {
		t.OpenID = result.UserInfo.OpenID
	}
	t.mu.Unlock()
	if err := m.persist(t); err != nil {
		t.mu.Lock()
		t.Status = "failed"
		t.Err = err.Error()
		t.mu.Unlock()
	}
}
func (m *Manager) snapshot(t *task) agentproto.FeishuAppRegistrationResponse {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return agentproto.FeishuAppRegistrationResponse{OK: t.Err == "", TaskID: t.ID, Status: t.Status, QRURL: t.QRURL, QRExpiresAt: t.QRExpiresAt, Interval: t.Interval, ClientID: t.ClientID, UserOpenID: t.OpenID, Error: t.Err}
}
func (m *Manager) status(id, tid string) agentproto.QueryResponse {
	m.mu.RLock()
	t := m.tasks[tid]
	m.mu.RUnlock()
	if t == nil {
		return response(id, 404, map[string]any{"ok": false, "error": "task-not-found"})
	}
	return response(id, 200, m.snapshot(t))
}
func (m *Manager) cancel(id, tid string) agentproto.QueryResponse {
	m.mu.RLock()
	t := m.tasks[tid]
	m.mu.RUnlock()
	if t == nil {
		return response(id, 404, map[string]any{"ok": false, "error": "task-not-found"})
	}
	t.mu.Lock()
	if t.Status == "completed" || t.Status == "failed" || t.Status == "cancelled" {
		t.mu.Unlock()
		return response(id, 409, map[string]any{"ok": false, "error": "task-not-active"})
	}
	t.Status = "cancelled"
	cancel := t.cancel
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return response(id, 200, m.snapshot(t))
}
func response(id string, status int, v any) agentproto.QueryResponse {
	b, _ := json.Marshal(v)
	return agentproto.QueryResponse{ID: id, StatusCode: status, Body: b}
}
func (m *Manager) persist(t *task) error {
	t.mu.RLock()
	p := t.params
	id, secret, openID := t.ClientID, t.ClientSecret, t.OpenID
	t.mu.RUnlock()
	if p.ReceiveOpenID == "" {
		p.ReceiveOpenID = openID
	}
	events := p.Events
	if len(events) == 0 {
		events = []string{"game_start", "game_end"}
	}
	data, err := os.ReadFile(m.configPath)
	if err != nil {
		return err
	}
	var root yaml.Node
	if err = yaml.Unmarshal(data, &root); err != nil {
		return err
	}
	if len(root.Content) == 0 {
		return fmt.Errorf("invalid yaml")
	}
	doc := root.Content[0]
	var targets *yaml.Node
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value == "TARGETS" {
			targets = doc.Content[i+1]
		}
	}
	if targets == nil {
		targets = &yaml.Node{Kind: yaml.SequenceNode}
		doc.Content = append(doc.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "TARGETS"}, targets)
	}
	out := yaml.Node{Kind: yaml.SequenceNode}
	for i := 0; i < len(targets.Content); i++ {
		item := targets.Content[i]
		keep := true
		for j := 0; j+1 < len(item.Content); j += 2 {
			if item.Content[j].Value == "TYPE" && item.Content[j+1].Value == "feishu" {
				keep = false
			}
		}
		if keep {
			out.Content = append(out.Content, item)
		}
	}
	item := &yaml.Node{Kind: yaml.MappingNode}
	add := func(k, v string) {
		item.Content = append(item.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, &yaml.Node{Kind: yaml.ScalarNode, Value: v})
	}
	add("ID", p.TargetID)
	add("TYPE", "feishu")
	add("APP_ID", id)
	add("APP_SECRET", secret)
	add("RECEIVE_OPEN_ID", p.ReceiveOpenID)
	add("LIVE_UPDATE", fmt.Sprint(p.LiveUpdate))
	ev := &yaml.Node{Kind: yaml.SequenceNode}
	for _, e := range events {
		ev.Content = append(ev.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: e})
	}
	item.Content = append(item.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "EVENTS"}, ev)
	out.Content = append(out.Content, item)
	targets.Content = out.Content
	var b strings.Builder
	if err = root.Encode(&b); err != nil {
		return err
	}
	tmp := m.configPath + ".tmp"
	if err = os.WriteFile(tmp, []byte(b.String()), 0600); err != nil {
		return err
	}
	if err = os.Rename(tmp, m.configPath); err != nil {
		_ = os.Remove(m.configPath)
		if err = os.Rename(tmp, m.configPath); err != nil {
			return err
		}
	}
	cfg, err := config.LoadAgentFile(m.configPath)
	if err != nil {
		return err
	}
	m.dispatcher.SetConfig(cfg.Webhook)
	return nil
}
