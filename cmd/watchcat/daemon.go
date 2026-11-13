package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/binarycat0/watchcat/internal/agent"
	"github.com/binarycat0/watchcat/internal/api"
	"github.com/binarycat0/watchcat/internal/config"
	"github.com/binarycat0/watchcat/internal/core"
	"github.com/binarycat0/watchcat/internal/github"
	"github.com/binarycat0/watchcat/internal/health"
	"github.com/binarycat0/watchcat/internal/secrets"
	"github.com/binarycat0/watchcat/internal/slack"
	"github.com/binarycat0/watchcat/internal/worker"
	"github.com/binarycat0/watchcat/web"
)

func cmdDaemon(e env, args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.StringVar(&e.cfg.Host, "host", e.cfg.Host, "API/web host (loopback only)")
	fs.IntVar(&e.cfg.Port, "port", e.cfg.Port, "API/web port")
	if err := fs.Parse(args); err != nil {
		return err
	}
	listen := e.cfg.Addr()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if _, err := config.Init(e.paths); err != nil {
		return err
	}
	store, err := core.Open(e.paths.DB)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.Close()

	// Bind first so a second daemon fails fast instead of double-scheduling.
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("listen on %s (is another daemon running?): %w", listen, err)
	}
	if !isLoopback(ln.Addr()) {
		ln.Close()
		return fmt.Errorf("refusing to listen on non-loopback address %s: the API has no auth", ln.Addr())
	}

	// NotifyContext cancels ctx on Ctrl-C or SIGTERM (what launchd/systemd send).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Routers decide which follow-up tasks each PR change triggers. Future
	// workers (review resolver, agent hand-off) get appended here.
	// live holds the settings the web UI can change; workers read it on
	// every use instead of keeping startup copies.
	live := config.NewLive(e.cfg, e.paths)
	cfg := live.Get
	notifyOn := func() []string { return cfg().NotifyOn }
	slackTarget := func() string { return cfg().Slack.Target() }
	var routes []worker.Router
	if e.tokenErr != nil {
		log.Warn("secret store", "err", e.tokenErr)
	}
	if e.tokenSource == secrets.FromConfig {
		log.Warn("Slack token is stored in plaintext in config.json; move it with `watchcat secret set slack-token`")
	}
	var poster slack.Poster
	if e.cfg.Slack.BotToken != "" {
		poster = &slack.Bot{Token: e.cfg.Slack.BotToken}
		slackRoute := worker.SlackRouter(notifyOn)
		// Only queue messages while there is someone to send them to.
		routes = append(routes, func(pr core.PR, s core.Snapshot, c core.Change) []core.NewTask {
			if slackTarget() == "" {
				return nil
			}
			return slackRoute(pr, s, c)
		})
	} else {
		log.Warn("no Slack token; changes are recorded as events but not sent")
	}
	interval := func() time.Duration { return cfg().PollInterval.Duration }
	gh := github.GH{}
	oc := agent.Opencode{} // drives `opencode api` from each session's worktree
	method := func() string { return cfg().MergeMethod }
	pool := &worker.Pool{
		Store: store,
		Handlers: map[string]worker.Handler{
			worker.KindPRCheck: &worker.PRCheck{
				Store: store, GitHub: gh, Interval: interval, Runs: gh,
				Route: worker.Routers(routes...),
				Workflows: &worker.Workflows{
					Store:  store,
					Modes:  func() map[string]string { return cfg().Workflows },
					Notify: func() bool { return poster != nil && slackTarget() != "" },
				},
			},
			worker.KindPRMerge:     &worker.Merge{Store: store, GitHub: gh, Writer: gh, Method: method},
			worker.KindPRAutoMerge: &worker.AutoMerge{Store: store, GitHub: gh, Writer: gh, Method: method},
			worker.KindAgentPrompt: &worker.AgentPrompt{Store: store, GitHub: gh, Reader: gh, Agent: oc},
			worker.KindSlackNotify: &worker.SlackNotify{Slack: poster, Channel: slackTarget},
			worker.KindPRRetrigger: &worker.Retrigger{
				Store: store, GitHub: gh, RecheckAfter: time.Minute,
			},
		},
		Planners:    []worker.Planner{worker.PRPlanner(store, interval)},
		Tick:        e.cfg.TickInterval.Duration,
		MaxParallel: e.cfg.Workers,
		Log:         log,
	}
	srv := &api.Server{
		Store: store, Pool: pool, Web: web.FS(), Version: version, DBPath: e.paths.DB,
		Slack: poster, SlackChannel: slackTarget, Live: live, Log: log, Agent: oc,
		GitHubHosts: func(ctx context.Context) ([]string, error) {
			accounts, err := gh.Accounts(ctx)
			return github.SignedInHosts(accounts), err
		},
		Health: func(ctx context.Context) health.Report {
			prs, _ := store.ListPRs(ctx)
			return health.Run(ctx, e.healthChecks(cfg(), prs)...)
		},
	}
	httpSrv := &http.Server{Handler: srv.Handler(os.Getpid()), ReadHeaderTimeout: 5 * time.Second}

	// Run the worker pool and HTTP server concurrently; done channels report
	// when each has finished.
	if n, err := worker.BackfillLifecycle(ctx, store); err != nil {
		log.Warn("report backfill", "err", err)
	} else if n > 0 {
		log.Info("re-checking PRs to fill in report timestamps", "count", n)
	}
	poolDone := make(chan struct{})
	go func() {
		pool.Run(ctx)
		close(poolDone)
	}()
	httpErr := make(chan error, 1)
	go func() { httpErr <- httpSrv.Serve(ln) }()

	log.Info("watchcat daemon started", "version", version, "listen", "http://"+ln.Addr().String(), "db", e.paths.DB)

	select {
	case <-ctx.Done():
	case err := <-httpErr:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server", "err", err)
		}
		stop()
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	httpSrv.Shutdown(shutdownCtx)
	<-poolDone
	return nil
}

func isLoopback(a net.Addr) bool {
	tcp, ok := a.(*net.TCPAddr) // type assertion: like isinstance + cast in one step
	return ok && tcp.IP.IsLoopback()
}
