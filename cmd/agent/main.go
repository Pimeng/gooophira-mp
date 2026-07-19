// Command agent runs optional asynchronous extensions beside the game server.
// Stage A establishes the authenticated local IPC lifecycle; business event
// consumers are added in subsequent migration stages.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/agentinbox"
	"github.com/Pimeng/gooophira-mp/internal/agentipc"
	"github.com/Pimeng/gooophira-mp/internal/agentproto"
	"github.com/Pimeng/gooophira-mp/internal/agentstats"
	"github.com/Pimeng/gooophira-mp/internal/agentupload"
	"github.com/Pimeng/gooophira-mp/internal/agentwebhook"
	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/stats"
	"github.com/Pimeng/gooophira-mp/internal/version"
	"github.com/Pimeng/gooophira-mp/internal/webhook"
)

type agentLogger struct{}

func (agentLogger) Debug(message string) { log.Printf("DEBUG %s", message) }
func (agentLogger) Warn(message string)  { log.Printf("WARN %s", message) }

type eventProcessor interface {
	Process(context.Context, int) (int, error)
	Cursor() uint64
}

func main() {
	discoveryPath := flag.String("discovery", "agent-ipc.json", "server Agent IPC discovery file")
	consumerID := flag.String("consumer", "default", "stable Agent consumer identity")
	retryDelay := flag.Duration("retry-delay", 2*time.Second, "delay between connection attempts")
	inboxPath := flag.String("inbox", "agent-inbox/events.log", "durable Agent event inbox")
	inboxMaxMB := flag.Int("inbox-max-mb", 256, "durable Agent inbox capacity in MiB")
	configPath := flag.String("config", "config/agent.yaml", "Agent-owned extension configuration")
	legacyConfigPath := flag.String("webhook-config", "", "deprecated alias for -config")
	flag.Parse()
	agentConfigPath := resolveAgentConfigPath(*configPath, *legacyConfigPath)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	inbox, err := agentinbox.Open(*inboxPath, int64(*inboxMaxMB)*1024*1024)
	if err != nil {
		log.Fatalf("open Agent inbox: %v", err)
	}
	defer inbox.Close()

	agentConfig, err := config.LoadAgentFile(agentConfigPath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Fatalf("load Agent configuration: %v", err)
		}
		agentConfig = &config.AgentConfig{Webhook: &config.WebhookConfig{}}
	}
	var processors []eventProcessor
	webhookDispatcher := webhook.New(agentLogger{}, l10n.NewLanguage(""))
	webhookDispatcher.SetConfig(agentConfig.Webhook)
	defer webhookDispatcher.Close()
	if agentConfig.Webhook != nil && agentConfig.Webhook.Enabled && len(agentConfig.Webhook.Targets) > 0 {
		webhookLedger, err := agentwebhook.OpenLedger(*inboxPath + ".webhook-ledger")
		if err != nil {
			log.Fatalf("open Agent webhook ledger: %v", err)
		}
		defer webhookLedger.Close()
		webhookProcessor, err := agentwebhook.OpenProcessor(inbox, webhookDispatcher, webhookDispatcher, webhookLedger, *inboxPath+".webhook-cursor")
		if err != nil {
			log.Fatalf("open Agent webhook processor: %v", err)
		}
		processors = append(processors, webhookProcessor)
	}
	var statsStore *stats.Store
	if agentConfig.Stats.Enabled {
		statsStore, err = stats.Open(agentConfig.Stats.DBPath)
		if err != nil {
			log.Fatalf("open Agent stats database: %v", err)
		}
		defer statsStore.Close()
		statsProcessor, err := agentstats.OpenProcessor(inbox, statsStore, *inboxPath+".stats-cursor")
		if err != nil {
			log.Fatalf("open Agent stats processor: %v", err)
		}
		processors = append(processors, statsProcessor)
		if err := statsStore.CleanupDetail(agentConfig.Stats.DetailRetentionDays); err != nil {
			log.Printf("WARN stats cleanup failed: %v", err)
		}
		maintenanceCtx, maintenanceCancel := context.WithCancel(ctx)
		maintenanceDone := make(chan struct{})
		go maintainStats(maintenanceCtx, statsStore, agentConfig.Stats, maintenanceDone)
		defer func() {
			maintenanceCancel()
			<-maintenanceDone
		}()
	}
	var uploadStore *agentupload.Store
	if agentConfig.ReplayUpload.Enabled {
		uploadStore, err = agentupload.Open(agentConfig.ReplayUpload)
		if err != nil {
			log.Fatalf("open Agent replay upload store: %v", err)
		}
		uploadProcessor, err := agentupload.OpenProcessor(inbox, uploadStore, *inboxPath+".upload-cursor")
		if err != nil {
			log.Fatalf("open Agent replay upload processor: %v", err)
		}
		processors = append(processors, uploadProcessor)
		go uploadStore.Run(ctx)
	}

	queryHandler := queryHandlers{stats: agentstats.QueryHandler{Store: statsStore}, upload: agentupload.QueryHandler{Store: uploadStore}}
	if err := run(ctx, *discoveryPath, *consumerID, *retryDelay, inbox, processors, queryHandler); err != nil {
		log.Printf("agent stopped: %v", err)
	}
}

func resolveAgentConfigPath(configPath, legacyFlagPath string) string {
	return resolveAgentConfigPathWithStat(configPath, legacyFlagPath, func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	})
}

func resolveAgentConfigPathWithStat(configPath, legacyFlagPath string, exists func(string) bool) string {
	if legacyFlagPath != "" {
		log.Printf("WARN -webhook-config is deprecated; use -config")
		return legacyFlagPath
	}
	if configPath != "config/agent.yaml" {
		return configPath
	}
	if exists(configPath) {
		return configPath
	}
	const legacyDefault = "agent_config.yml"
	if exists(legacyDefault) {
		log.Printf("WARN %s is deprecated; move it to %s", legacyDefault, configPath)
		return legacyDefault
	}
	return configPath
}

func maintainStats(ctx context.Context, store *stats.Store, cfg config.AgentStatsConfig, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := store.CleanupDetail(cfg.DetailRetentionDays); err != nil {
				log.Printf("WARN stats cleanup failed: %v", err)
			}
			if err := store.VacuumIfNeeded(cfg.DBPath, cfg.DBMaxMB); err != nil {
				log.Printf("WARN stats vacuum failed: %v", err)
			}
		}
	}
}

type queryHandlers struct {
	stats  agentstats.QueryHandler
	upload agentupload.QueryHandler
}

func (h queryHandlers) Handle(ctx context.Context, request agentproto.QueryRequest) agentproto.QueryResponse {
	if response, handled := h.upload.Handle(ctx, request); handled {
		return response
	}
	return h.stats.Handle(request)
}

func run(ctx context.Context, discoveryPath, consumerID string, retryDelay time.Duration, inbox *agentinbox.Store, processors []eventProcessor, queryHandler queryHandlers) error {
	if consumerID == "" {
		return fmt.Errorf("consumer identity must not be empty")
	}
	if retryDelay <= 0 {
		retryDelay = 2 * time.Second
	}
	connected := false
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		discovery, err := agentipc.ReadDiscovery(discoveryPath)
		if err != nil {
			if connected {
				log.Printf("server discovery unavailable: %v", err)
				connected = false
			}
			if !wait(ctx, retryDelay) {
				return nil
			}
			continue
		}
		client, err := agentipc.NewClient(discovery.Endpoint, discovery.Token, consumerID)
		if err != nil {
			return err
		}
		response, err := client.Handshake(ctx, version.Get(), []string{"health.v1", "events.v1"})
		if err != nil {
			client.Close()
			if connected {
				log.Printf("server connection lost: %v", err)
				connected = false
			}
			if !wait(ctx, retryDelay) {
				return nil
			}
			continue
		}
		if !connected {
			log.Printf("connected to server %s using Agent protocol v%d", response.ServerVersion, response.ProtocolVersion)
			connected = true
		}
		if inbox.LastSequence() < response.AckedSequence {
			if err := inbox.SetBaseline(response.AckedSequence); err != nil {
				client.Close()
				return fmt.Errorf("Agent inbox ends at %d but server already ACKed %d: %w", inbox.LastSequence(), response.AckedSequence, err)
			}
		}
		connectionCtx, connectionCancel := context.WithCancel(ctx)
		queryDone := make(chan error, 1)
		go func() { queryDone <- pollQueries(connectionCtx, client, queryHandler) }()
		err = poll(ctx, client, inbox, processors, response.AckedSequence)
		connectionCancel()
		queryErr := <-queryDone
		if err == nil && queryErr != nil && ctx.Err() == nil {
			err = queryErr
		}
		client.Close()
		if err != nil && ctx.Err() == nil {
			log.Printf("server connection lost: %v", err)
			connected = false
		}
		if !wait(ctx, retryDelay) {
			return nil
		}
	}
}

func pollQueries(ctx context.Context, client *agentipc.Client, handler queryHandlers) error {
	for {
		requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		query, ok, err := client.NextQuery(requestCtx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if !ok {
			if !wait(ctx, 200*time.Millisecond) {
				return nil
			}
			continue
		}
		response := handler.Handle(ctx, query)
		requestCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
		err = client.QueryResult(requestCtx, response)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}

func poll(ctx context.Context, client *agentipc.Client, inbox *agentinbox.Store, processors []eventProcessor, acked uint64) error {
	lastHealth := time.Time{}
	for {
		requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		batch, err := client.Events(requestCtx, acked, 100)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if batch.AckedSequence != acked {
			return fmt.Errorf("server ACK changed from %d to %d", acked, batch.AckedSequence)
		}
		if len(batch.Events) > 0 {
			durable, err := inbox.Accept(batch.Events)
			if err != nil {
				return err
			}
			last := batch.Events[len(batch.Events)-1].Sequence
			if durable < last {
				return fmt.Errorf("Agent inbox persisted through %d, expected %d", durable, last)
			}
		}
		for _, processor := range processors {
			for {
				processed, err := processor.Process(ctx, 100)
				if err != nil {
					return err
				}
				if processed == 0 {
					break
				}
			}
		}
		if len(batch.Events) > 0 {
			last := batch.Events[len(batch.Events)-1].Sequence
			for _, processor := range processors {
				if processor.Cursor() < last {
					return fmt.Errorf("Agent processor reached %d, expected %d before ACK", processor.Cursor(), last)
				}
			}
			requestCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
			err = client.Ack(requestCtx, last)
			cancel()
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			acked = last
			continue
		}
		if time.Since(lastHealth) >= 10*time.Second {
			requestCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
			_, err = client.Health(requestCtx)
			cancel()
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			lastHealth = time.Now()
		}
		if !wait(ctx, time.Second) {
			return nil
		}
	}
}

func wait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
