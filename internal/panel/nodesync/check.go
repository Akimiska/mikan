package nodesync

import (
	"context"
	"net"
	"time"

	"mikan/internal/nodeapi"
	"mikan/internal/nodehello"
)

// CheckNow asks a node for its health now instead of at the next tick, and returns the
// view the Nodes page then shows.
func (m *Manager) CheckNow(ctx context.Context, id int64) (HealthView, bool) {
	s, ok := m.Syncer(id)
	if !ok {
		return HealthView{}, false
	}
	s.refreshHealth(ctx)
	return s.Health(), true
}

// HelloView is a node's last hello: when it came and how the panel's dial back went.
type HelloView struct {
	At     time.Time
	Result nodehello.Result
}

// RecordHello keeps a node's hello for the Nodes page. Only in memory: a hello matters
// while somebody installs the node.
func (m *Manager) RecordHello(id int64, r nodehello.Result, at time.Time) {
	m.helloMu.Lock()
	defer m.helloMu.Unlock()
	if m.hellos == nil {
		m.hellos = map[int64]HelloView{}
	}
	m.hellos[id] = HelloView{At: at, Result: r}
}

// Hello is a node's last hello since the panel started.
func (m *Manager) Hello(id int64) (HelloView, bool) {
	m.helloMu.Lock()
	defer m.helloMu.Unlock()
	v, ok := m.hellos[id]
	return v, ok
}

// forgetHello drops what a removed node said.
func (m *Manager) forgetHello(id int64) {
	m.helloMu.Lock()
	defer m.helloMu.Unlock()
	delete(m.hellos, id)
}

// Diagnose asks a node how its server is (nodeapi.Client.Diagnose).
func (m *Manager) Diagnose(ctx context.Context, id int64) (nodeapi.Diagnosis, error) {
	c, err := clientOf[interface {
		Diagnose(context.Context) (nodeapi.Diagnosis, error)
	}](m, id)
	if err != nil {
		return nodeapi.Diagnosis{}, err
	}
	return c.Diagnose(ctx)
}

// Logs asks a node for the last tail lines of its log (nodeapi.Client.Logs).
func (m *Manager) Logs(ctx context.Context, id int64, tail int) (nodeapi.Logs, error) {
	c, err := clientOf[interface {
		Logs(context.Context, int) (nodeapi.Logs, error)
	}](m, id)
	if err != nil {
		return nodeapi.Logs{}, err
	}
	return c.Logs(ctx, tail)
}

// HostDoctor has a remote node's server run `mikan doctor` (nodeapi.Client.HostDoctor).
func (m *Manager) HostDoctor(ctx context.Context, id int64) (nodeapi.HostReport, error) {
	c, err := clientOf[interface {
		HostDoctor(context.Context) (nodeapi.HostReport, error)
	}](m, id)
	if err != nil {
		return nodeapi.HostReport{}, err
	}
	return c.HostDoctor(ctx)
}

// Terminal opens a support terminal session on a remote node's server and returns its frame
// stream (nodeapi.Client.Terminal); the caller has checked the grant.
func (m *Manager) Terminal(ctx context.Context, id int64, o nodeapi.TerminalOpen) (net.Conn, error) {
	c, err := clientOf[interface {
		Terminal(context.Context, nodeapi.TerminalOpen) (net.Conn, error)
	}](m, id)
	if err != nil {
		return nil, err
	}
	return c.Terminal(ctx, o)
}

// Recordings lists the terminal recordings on a remote node's server.
func (m *Manager) Recordings(ctx context.Context, id int64) (nodeapi.RecordingList, error) {
	c, err := clientOf[interface {
		Recordings(context.Context) (nodeapi.RecordingList, error)
	}](m, id)
	if err != nil {
		return nodeapi.RecordingList{}, err
	}
	return c.Recordings(ctx)
}

// Recording returns one terminal recording of a remote node's server.
func (m *Manager) Recording(ctx context.Context, id int64, name string) ([]byte, error) {
	c, err := clientOf[interface {
		Recording(context.Context, string) ([]byte, error)
	}](m, id)
	if err != nil {
		return nil, err
	}
	return c.Recording(ctx, name)
}

// HostRestart has a remote node's server restart its containers (nodeapi.Client.HostRestart).
func (m *Manager) HostRestart(ctx context.Context, id int64) (nodeapi.RestartAccepted, error) {
	c, err := clientOf[interface {
		HostRestart(context.Context) (nodeapi.RestartAccepted, error)
	}](m, id)
	if err != nil {
		return nodeapi.RestartAccepted{}, err
	}
	return c.HostRestart(ctx)
}
