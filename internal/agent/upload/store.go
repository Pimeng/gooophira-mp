package agentupload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/agent/integration/sharestation"
	"github.com/Pimeng/gooophira-mp/internal/common/platform/securepath"
	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/core/replay"
)

type uploadClient interface {
	Upload([]byte, string, string, string) (sharestation.UploadResult, error)
	SetVisibility(int, bool) error
}

type uploadJob struct {
	ReplayID string    `json:"replay_id"`
	DueAt    time.Time `json:"due_at"`
}

type UploadResult struct {
	ReplayID string `json:"replay_id"`
	ScoreID  int    `json:"score_id"`
	RecordID int    `json:"record_id"`
}

type diskState struct {
	Version  int                     `json:"version"`
	Show     map[string]bool         `json:"show"`
	Jobs     map[string]uploadJob    `json:"jobs"`
	Results  map[string]UploadResult `json:"results"`
	Attempts map[string]int          `json:"attempts"`
}

type Store struct {
	mu       sync.Mutex
	uploadMu sync.Mutex
	cfg      config.AgentReplayUploadConfig
	client   uploadClient
	state    diskState
}

func Open(cfg config.AgentReplayUploadConfig) (*Store, error) {
	client := sharestation.NewClient(sharestation.Config{URL: cfg.URL, Token: cfg.Token})
	return openWithClient(cfg, client)
}

func openWithClient(cfg config.AgentReplayUploadConfig, client uploadClient) (*Store, error) {
	s := &Store{cfg: cfg, client: client, state: diskState{Version: 1, Show: map[string]bool{}, Jobs: map[string]uploadJob{}, Results: map[string]UploadResult{}, Attempts: map[string]int{}}}
	data, err := os.ReadFile(cfg.StatePath)
	if err == nil {
		if err := json.Unmarshal(data, &s.state); err != nil {
			return nil, fmt.Errorf("decode replay upload state: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if s.state.Show == nil {
		s.state.Show = map[string]bool{}
	}
	if s.state.Jobs == nil {
		s.state.Jobs = map[string]uploadJob{}
	}
	if s.state.Results == nil {
		s.state.Results = map[string]UploadResult{}
	}
	if s.state.Attempts == nil {
		s.state.Attempts = map[string]int{}
	}
	return s, nil
}

func (s *Store) Schedule(replayID string, createdAt time.Time) error {
	if _, err := replay.ParseID(replayID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, done := s.state.Results[replayID]; done {
		return nil
	}
	if _, queued := s.state.Jobs[replayID]; queued {
		return nil
	}
	s.state.Jobs[replayID] = uploadJob{ReplayID: replayID, DueAt: createdAt.Add(time.Duration(s.cfg.DelayMS) * time.Millisecond)}
	return s.saveLocked()
}

func (s *Store) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		s.runDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Store) runDue(ctx context.Context) {
	s.mu.Lock()
	var due []string
	for id, job := range s.state.Jobs {
		if !job.DueAt.After(time.Now()) {
			due = append(due, id)
		}
	}
	s.mu.Unlock()
	for _, id := range due {
		parsed, err := replay.ParseID(id)
		if err != nil {
			continue
		}
		show, _ := s.AutoConfig(parsed.UserID, nil)
		if _, err := s.Upload(ctx, id, show); err != nil {
			s.reschedule(id)
		}
	}
}

func (s *Store) reschedule(replayID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.state.Jobs[replayID]
	if !ok {
		return
	}
	attempt := s.state.Attempts[replayID]
	if attempt > 8 {
		attempt = 8
	}
	delay := time.Minute * time.Duration(1<<attempt)
	job.DueAt = time.Now().Add(delay)
	s.state.Jobs[replayID] = job
	_ = s.saveLocked()
}

func (s *Store) Upload(ctx context.Context, replayID string, visible bool) (UploadResult, error) {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	if err := ctx.Err(); err != nil {
		return UploadResult{}, err
	}
	id, err := replay.ParseID(replayID)
	if err != nil {
		return UploadResult{}, err
	}
	path, err := id.Path(s.cfg.BaseDir)
	if err != nil {
		return UploadResult{}, err
	}
	s.mu.Lock()
	if result, ok := s.state.Results[replayID]; ok {
		s.mu.Unlock()
		if visible {
			_ = s.client.SetVisibility(result.ScoreID, true)
		}
		_ = os.Remove(path)
		return result, nil
	}
	s.state.Attempts[replayID]++
	if err := s.saveLocked(); err != nil {
		s.mu.Unlock()
		return UploadResult{}, err
	}
	s.mu.Unlock()
	header, err := replay.ReadReplayHeader(path)
	if err != nil || header.UserID != id.UserID || header.ChartID != id.ChartID {
		return UploadResult{}, fmt.Errorf("replay not found or invalid")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return UploadResult{}, err
	}
	remote, err := s.client.Upload(data, strconv.FormatInt(id.Timestamp, 10)+".phirarec", header.ChartName, header.UserName)
	if err != nil {
		return UploadResult{}, err
	}
	if remote.ScoreID == 0 {
		return UploadResult{}, fmt.Errorf("upload response has no score ID")
	}
	result := UploadResult{ReplayID: remote.ReplayID, ScoreID: remote.ScoreID, RecordID: header.RecordID}
	s.mu.Lock()
	s.state.Results[replayID] = result
	delete(s.state.Jobs, replayID)
	err = s.saveLocked()
	s.mu.Unlock()
	if err != nil {
		return UploadResult{}, err
	}
	if visible {
		_ = s.client.SetVisibility(remote.ScoreID, true)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	return result, nil
}

func (s *Store) AutoConfig(userID int, show *bool) (bool, error) {
	if userID < 0 {
		return false, fmt.Errorf("invalid user ID")
	}
	key := strconv.Itoa(userID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if show != nil {
		s.state.Show[key] = *show
		if err := s.saveLocked(); err != nil {
			return false, err
		}
	}
	return s.state.Show[key], nil
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.cfg.StatePath)
	if err := os.MkdirAll(dir, 0o700); err != nil && dir != "." {
		return err
	}
	if dir != "." {
		if err := securepath.RestrictToCurrentUser(dir); err != nil {
			return err
		}
	}
	temp, err := os.CreateTemp(dir, ".upload-state-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := securepath.RestrictToCurrentUser(tempPath); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return replaceFile(tempPath, s.cfg.StatePath)
}
