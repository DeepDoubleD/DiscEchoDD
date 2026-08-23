// Command discecho is the DiscEcho daemon entrypoint. It opens the
// SQLite store, loads settings, wires the tool/pipeline registries and
// the orchestrator, then serves the HTTP API while listening for udev
// optical-media-change events.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/api"
	"github.com/jumpingmushroom/DiscEcho/daemon/drive"
	"github.com/jumpingmushroom/DiscEcho/daemon/embed"
	"github.com/jumpingmushroom/DiscEcho/daemon/identify"
	"github.com/jumpingmushroom/DiscEcho/daemon/integrations"
	"github.com/jumpingmushroom/DiscEcho/daemon/jobs"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/audiocd"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/bdmv"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/cdgame"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/data"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/dreamcast"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/dvdvideo"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/ps2"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/psx"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/saturn"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/uhd"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/vcd"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/xbox"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/xbox360"
	"github.com/jumpingmushroom/DiscEcho/daemon/settings"
	"github.com/jumpingmushroom/DiscEcho/daemon/spool"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
	"github.com/jumpingmushroom/DiscEcho/daemon/version"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	dataPath := firstEnv("DISCECHO_DATA", "/var/lib/discecho")
	if err := os.MkdirAll(dataPath, 0o700); err != nil {
		slog.Error("mkdir data", "err", err)
		os.Exit(1)
	}
	dbPath := filepath.Join(dataPath, "discecho.sqlite")
	db, err := state.Open(dbPath)
	if err != nil {
		slog.Error("state.Open", "err", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	store := state.NewStore(db)
	bc := state.NewBroadcaster()
	defer bc.Close()

	cfg, err := settings.Load(os.Getenv, store, version.Info().Version)
	if err != nil {
		slog.Error("settings.Load", "err", err)
		os.Exit(1)
	}
	slog.Info("settings loaded",
		"addr", cfg.Addr,
		"library_movies", cfg.LibraryMovies,
		"library_tv", cfg.LibraryTV,
		"library_music", cfg.LibraryMusic,
		"library_games", cfg.LibraryGames,
		"library_data", cfg.LibraryData,
	)
	if cfg.Token != "" {
		slog.Info("bearer auth enabled")
	} else {
		slog.Info("auth disabled (LAN mode); set DISCECHO_TOKEN to enable bearer auth")
	}

	if n, err := drive.InitialScan(context.Background(), store); err != nil {
		slog.Warn("InitialScan", "err", err)
	} else {
		slog.Info("drives discovered", "count", n)
	}
	// Recover any drive left in `identifying` by a previous run. Without
	// this, ClaimDriveForIdentify (which only transitions from idle/error)
	// refuses every later uevent and the daemon stays deaf on that drive.
	if n, err := store.ResetIdentifyingDrives(context.Background()); err != nil {
		slog.Warn("ResetIdentifyingDrives", "err", err)
	} else if n > 0 {
		slog.Info("reset stuck identifying drives", "count", n)
	}

	// Tools — Whipper for ripping, Apprise for notifications, Eject
	// for the post-rip eject step. Metaflac + Loudgain are best-effort
	// audio-CD post-processing (cover-art embed + ReplayGain); both
	// degrade to WARN+continue if the binary is missing.
	toolReg := tools.NewRegistry()
	toolReg.Register(tools.NewWhipper(cfg.WhipperBin))
	appriseTool := tools.NewApprise(cfg.AppriseBin)
	toolReg.Register(appriseTool)
	ejectTool := tools.NewEject(cfg.EjectBin)
	toolReg.Register(ejectTool)
	toolReg.Register(tools.NewMetaflac(cfg.MetaflacBin))
	toolReg.Register(tools.NewLoudgain(cfg.LoudgainBin))

	// Identify (TOC + MusicBrainz)
	tocReader := identify.NewCDParanoiaTOCReader(cfg.CDParanoiaBin)
	mbClient := identify.NewMusicBrainzClient(identify.MusicBrainzConfig{
		BaseURL:     cfg.MusicBrainzBaseURL,
		UserAgent:   cfg.MusicBrainzUserAgent,
		MinInterval: time.Second,
	})
	sysCNFProber := identify.NewSystemCNFProber(cfg.IsoInfoBin)
	bdProber := identify.NewBDProber(identify.BDProberConfig{BDInfoBin: cfg.BDInfoBin})
	mkvSubs := tools.NewMKVToolNix(cfg.MKVMergeBin, cfg.MKVExtractBin)
	// xboxProber constructed here (not inline in pipeReg.Register below)
	// so the same instance can also be wired into the classifier --
	// previously ClassifierConfig.XboxProber was left unset entirely,
	// which meant RefineDiscType's Xbox branch was dead code: an Xbox
	// disc always fell through to DATA classification and never reached
	// the xbox pipeline handler at all.
	//
	// xbox360Prober has no classifier-side use: Xbox 360 discs are
	// classified by the /_SYSTEMU marker alone (see RefineDiscType) --
	// default.xex isn't reachable via a stock filesystem read on a real
	// disc, confirmed live, so a pre-rip XEX probe at classify time would
	// never succeed. The prober is still useful inside the xbox360
	// pipeline's own Identify() as a best-effort bonus path.
	xboxProber := &xbox.IsoinfoXboxProber{Bin: cfg.IsoInfoBin}
	xbox360Prober := &xbox360.IsoinfoXbox360Prober{Bin: cfg.IsoInfoBin}
	classifier := identify.NewClassifier(identify.ClassifierConfig{
		CDInfoBin:       cfg.CDInfoBin,
		FSProber:        identify.NewFSProber(identify.FSProberConfig{IsoInfoBin: cfg.IsoInfoBin}),
		BDProber:        bdProber,
		SystemCNFProber: sysCNFProber,
		CDGameProber:    identify.NewDevCDGameProber(),
		XboxProber:      xboxProber,
	})

	// urlsForTrigger is shared by every pipeline — looks up the
	// Apprise URLs configured for a given event trigger.
	urlsForTrigger := func(ctx context.Context, trigger string) []string {
		ns, err := store.ListNotificationsForTrigger(ctx, trigger)
		if err != nil {
			slog.Warn("notifications lookup", "trigger", trigger, "err", err)
			return nil
		}
		urls := make([]string, 0, len(ns))
		for _, n := range ns {
			urls = append(urls, n.URL)
		}
		return urls
	}

	// shouldEjectOnFinish is consulted by every pipeline at its rip-end
	// eject step. It reads operation.mode + rip.eject_on_finish at call
	// time so a settings change during a long rip takes effect on the
	// next job, not the next restart.
	shouldEjectOnFinish := func(ctx context.Context) bool {
		return pipelines.ShouldEjectOnFinish(ctx, store)
	}

	// Pipelines: register the audio-CD handler.
	pipeReg := pipelines.NewRegistry()
	pipeReg.Register(audiocd.New(audiocd.Deps{
		TOC:                  tocReader,
		MB:                   mbClient,
		Tools:                toolReg,
		LibraryRoot:          cfg.LibraryMusic,
		WorkRoot:             filepath.Join(cfg.DataPath, "work"),
		URLsForTrigger:       urlsForTrigger,
		ShouldEject:          shouldEjectOnFinish,
		MusicBrainzBaseURL:   cfg.MusicBrainzBaseURL,
		MusicBrainzUserAgent: cfg.MusicBrainzUserAgent,
	}))

	// DVD-Video pipeline
	tmdbClient := identify.NewTMDBClient(identify.TMDBConfig{
		APIKey:   cfg.TMDBKey,
		Language: cfg.TMDBLang,
	})
	dvdProber := identify.NewDVDProber(identify.DVDProberConfig{IsoInfoBin: cfg.IsoInfoBin})
	handBrake := tools.NewHandBrake(cfg.HandBrakeBin)
	toolReg.Register(handBrake)
	nvencAvailable := tools.ProbeNVENC(cfg.HandBrakeBin)
	if nvencAvailable {
		slog.Info("NVENC detected; hardware transcoding available")
	} else {
		slog.Info("NVENC not detected; profiles requesting nvenc_* will fall back to software")
	}

	// MakeMKV is shared by BDMV, UHD, and (since v0.26) DVD-Video profiles
	// whose engine is "MakeMKV" or "MakeMKV+HandBrake". DVD-Video profiles
	// on the legacy "HandBrake" engine still rip via dvdbackup +
	// libdvdcss — kept as the seeded default since libdvdcss handles
	// the vast majority of catalogue discs without depending on the
	// rolling MakeMKV beta key.
	makeMKV := tools.NewMakeMKV(cfg.MakeMKVBin, cfg.MakeMKVDataDir)
	dvdBackup := tools.NewDVDBackup("")

	// DVD pipeline shares one root for movies and series. The
	// orchestrator can't yet differentiate at job time, so series land
	// under library.movies alongside films. Routing DVD-Series to
	// library.tv requires per-job profile lookup in the dispatcher —
	// tracked for branch 3.
	pipeReg.Register(dvdvideo.New(dvdvideo.Deps{
		Prober:           dvdProber,
		TMDB:             tmdbClient,
		DVDBackup:        dvdBackup,
		HandBrakeScanner: handBrake,
		MakeMKVScanner:   makeMKV,
		MakeMKVRipper:    makeMKV,
		Tools:            toolReg,
		LibraryRoot:      cfg.LibraryMovies,
		LibraryTV:        cfg.LibraryTV,
		WorkRoot:         filepath.Join(cfg.DataPath, "work"),
		SubsLang:         cfg.SubsLang,
		URLsForTrigger:   urlsForTrigger,
		MetadataStore:    store,
		NVENCAvailable:   nvencAvailable,
		ShouldEject:      shouldEjectOnFinish,
		MKVSubs:          mkvSubs,
	}))

	// BDMV + UHD pipelines (M3.1).

	pipeReg.Register(bdmv.New(bdmv.Deps{
		Prober:         dvdProber, // re-used for volume-label reading
		BDProber:       bdProber,  // preferred: real disc-library title from bdmt_*.xml
		TMDB:           tmdbClient,
		MakeMKVScanner: makeMKV,
		MakeMKVRipper:  makeMKV,
		Tools:          toolReg,
		LibraryRoot:    cfg.LibraryMovies,
		LibraryTV:      cfg.LibraryTV,
		WorkRoot:       filepath.Join(cfg.DataPath, "work"),
		SubsLang:       cfg.SubsLang,
		URLsForTrigger: urlsForTrigger,
		NVENCAvailable: nvencAvailable,
		ShouldEject:    shouldEjectOnFinish,
		MKVSubs:        mkvSubs,
	}))

	pipeReg.Register(uhd.New(uhd.Deps{
		Prober:         dvdProber,
		BDProber:       bdProber, // preferred: real disc-library title from bdmt_*.xml
		TMDB:           tmdbClient,
		MakeMKVScanner: makeMKV,
		MakeMKVRipper:  makeMKV,
		Tools:          toolReg,
		LibraryRoot:    cfg.LibraryMovies,
		LibraryTV:      cfg.LibraryTV,
		WorkRoot:       filepath.Join(cfg.DataPath, "work"),
		SubsLang:       cfg.SubsLang,
		AACS2KeyDB:     filepath.Join(cfg.MakeMKVDataDir, "KEYDB.cfg"),
		URLsForTrigger: urlsForTrigger,
		ShouldEject:    shouldEjectOnFinish,
		MKVSubs:        mkvSubs,
	}))

	// PSX + PS2 pipelines (M5.1).
	redumperTool := tools.NewRedumper(cfg.RedumperBin)
	chdmanTool := tools.NewCHDMan(cfg.CHDManBin)

	redumpDB, err := identify.LoadRedumpDir(cfg.RedumpDataDir)
	if err != nil {
		slog.Warn("redump dir not loaded", "dir", cfg.RedumpDataDir, "err", err)
		redumpDB = nil
	}

	// BootCodeIndex: embedded per-system game DBs (PCSX2 / DuckStation /
	// Libretro). Loaded once at startup; missing or corrupt files are
	// non-fatal — auto-id silently degrades, IGDB manual search still works.
	bootCodeIndex, bootCodeErrs := identify.LoadAllEmbedded()
	for sys, bcErr := range bootCodeErrs {
		slog.Warn("boot-code map not loaded", "system", sys, "err", bcErr)
	}

	// IGDB client for game-disc manual search. Empty credentials produce a
	// not-Configured() client; the API dispatcher returns 503 cleanly.
	igdbClient := identify.NewIGDBClient(identify.IGDBConfig{
		ClientID:     cfg.IGDBClientID,
		ClientSecret: cfg.IGDBClientSecret,
		MinInterval:  250 * time.Millisecond,
	})

	// Integrations registry: resolves DB > env for each credential surface
	// and provides the runtime swap seam. First Put runs Reconfigure with
	// the resolved creds so the clients are initialized through the same
	// code path as a runtime edit.
	integReg := integrations.NewRegistry()

	envForIntegration := func(name string) (map[string]string, integrations.Source) {
		switch name {
		case "igdb":
			envCreds := map[string]string{
				"client_id":     cfg.IGDBClientID,
				"client_secret": cfg.IGDBClientSecret,
			}
			if envCreds["client_id"] == "" && envCreds["client_secret"] == "" {
				return map[string]string{}, integrations.SourceUnset
			}
			return envCreds, integrations.SourceEnv
		case "tmdb":
			envCreds := map[string]string{
				"key":  cfg.TMDBKey,
				"lang": cfg.TMDBLang,
			}
			if envCreds["key"] == "" {
				return map[string]string{}, integrations.SourceUnset
			}
			return envCreds, integrations.SourceEnv
		case "makemkv":
			envCreds := map[string]string{"beta_key": cfg.MakeMKVBetaKey}
			if envCreds["beta_key"] == "" {
				return map[string]string{}, integrations.SourceUnset
			}
			return envCreds, integrations.SourceEnv
		}
		return map[string]string{}, integrations.SourceUnset
	}

	bootCtx := context.Background()
	type integSpec struct {
		name        string
		reconfigure integrations.Reconfigure
	}
	for _, spec := range []integSpec{
		{"igdb", integrations.IGDBAdapter(igdbClient)},
		{"tmdb", integrations.TMDBAdapter(tmdbClient)},
		{"makemkv", integrations.MakeMKVAdapter(cfg.MakeMKVDataDir)},
	} {
		envCreds, _ := envForIntegration(spec.name)
		creds, source, err := integrations.Resolve(bootCtx, store, spec.name, envCreds)
		if err != nil {
			slog.Error("integrations resolve", "name", spec.name, "err", err)
			continue
		}
		if err := integReg.Put(spec.name, creds, source, spec.reconfigure); err != nil {
			slog.Error("integrations init reconfigure", "name", spec.name, "err", err)
		}
	}

	// Dreamcast IP.BIN reader: opens the block device and seeks to LBA 45000
	// to extract the product number (Sega GD-ROM HD area).
	dcIPBin := identify.NewDCIPBinReader()

	pipeReg.Register(psx.New(psx.Deps{
		Redumper:       redumperTool,
		CHDMan:         chdmanTool,
		SystemCNF:      sysCNFProber,
		RedumpDB:       redumpDB,
		BootCodeIndex:  bootCodeIndex,
		Tools:          toolReg,
		LibraryRoot:    cfg.LibraryGames,
		WorkRoot:       filepath.Join(cfg.DataPath, "work"),
		URLsForTrigger: urlsForTrigger,
		ShouldEject:    shouldEjectOnFinish,
	}))
	pipeReg.Register(ps2.New(ps2.Deps{
		Redumper:       redumperTool,
		CHDMan:         chdmanTool,
		SystemCNF:      sysCNFProber,
		RedumpDB:       redumpDB,
		BootCodeIndex:  bootCodeIndex,
		Tools:          toolReg,
		LibraryRoot:    cfg.LibraryGames,
		WorkRoot:       filepath.Join(cfg.DataPath, "work"),
		URLsForTrigger: urlsForTrigger,
		ShouldEject:    shouldEjectOnFinish,
	}))
	pipeReg.Register(saturn.New(saturn.Deps{
		Redumper:       redumperTool,
		CHDMan:         chdmanTool,
		SaturnProber:   identify.NewDevSaturnProber(),
		RedumpDB:       redumpDB,
		BootCodeIndex:  bootCodeIndex,
		Tools:          toolReg,
		LibraryRoot:    cfg.LibraryGames,
		WorkRoot:       filepath.Join(cfg.DataPath, "work"),
		URLsForTrigger: urlsForTrigger,
		ShouldEject:    shouldEjectOnFinish,
	}))
	pipeReg.Register(dreamcast.New(dreamcast.Deps{
		Redumper:       redumperTool,
		CHDMan:         chdmanTool,
		RedumpDB:       redumpDB,
		IPBin:          dcIPBin,
		BootCodeIndex:  bootCodeIndex,
		Tools:          toolReg,
		LibraryRoot:    cfg.LibraryGames,
		WorkRoot:       filepath.Join(cfg.DataPath, "work"),
		URLsForTrigger: urlsForTrigger,
		ShouldEject:    shouldEjectOnFinish,
	}))
	pipeReg.Register(xbox.New(xbox.Deps{
		Redumper:       redumperTool,
		XboxProber:     xboxProber,
		RedumpDB:       redumpDB,
		BootCodeIndex:  bootCodeIndex,
		Tools:          toolReg,
		LibraryRoot:    cfg.LibraryGames,
		WorkRoot:       filepath.Join(cfg.DataPath, "work"),
		URLsForTrigger: urlsForTrigger,
		ShouldEject:    shouldEjectOnFinish,
	}))
	pipeReg.Register(xbox360.New(xbox360.Deps{
		Redumper:       redumperTool,
		Xbox360Prober:  xbox360Prober,
		RedumpDB:       redumpDB,
		Tools:          toolReg,
		LibraryRoot:    cfg.LibraryGames,
		WorkRoot:       filepath.Join(cfg.DataPath, "work"),
		URLsForTrigger: urlsForTrigger,
		ShouldEject:    shouldEjectOnFinish,
	}))
	// CD-only consoles: no boot code, so PostRipIdentify = true (Redump MD5
	// lookup fills title/year after the rip). All rip via redumper CD mode →
	// .bin/.cue → .chd.
	for _, cd := range []struct {
		dt     state.DiscType
		prefix string
	}{
		{state.DiscTypeSegaCD, "segacd"},
		{state.DiscType3DO, "3do"},
		{state.DiscTypePCFX, "pcfx"},
		{state.DiscTypeJaguarCD, "jagcd"},
		{state.DiscTypeCDi, "cdi"},
		{state.DiscTypePCECD, "pcecd"},
		{state.DiscTypeNeoCD, "neocd"},
		{state.DiscTypeCD32, "cd32"},
		{state.DiscTypeFMTowns, "fmtowns"},
		{state.DiscTypePippin, "pippin"},
	} {
		pipeReg.Register(cdgame.New(cdgame.Deps{
			DiscType:        cd.dt,
			WorkPrefix:      cd.prefix,
			PostRipIdentify: true,
			Identifier:      cdgame.NoBootIdentifier{DiscType: cd.dt},
			Redumper:        redumperTool,
			CHDMan:          chdmanTool,
			RedumpDB:        redumpDB,
			Tools:           toolReg,
			LibraryRoot:     cfg.LibraryGames,
			WorkRoot:        filepath.Join(cfg.DataPath, "work"),
			URLsForTrigger:  urlsForTrigger,
			ShouldEject:     shouldEjectOnFinish,
		}))
	}

	pipeReg.Register(data.New(data.Deps{
		DD:             &tools.DDRescue{Bin: cfg.DDRescueBin},
		LabelProber:    &data.IsoinfoLabelProber{Bin: cfg.IsoInfoBin},
		Tools:          toolReg,
		LibraryRoot:    cfg.LibraryData,
		WorkRoot:       filepath.Join(cfg.DataPath, "work"),
		URLsForTrigger: urlsForTrigger,
		ShouldEject:    shouldEjectOnFinish,
	}))

	// VCD/SVCD → vcdxrip extracts the MPEG tracks (no transcode). Video
	// media, so it lands under the movies root alongside DVD/BD/UHD.
	pipeReg.Register(vcd.New(vcd.Deps{
		Ripper:         &tools.VCDXRip{Bin: cfg.VCDXRipBin},
		LabelProber:    &data.IsoinfoLabelProber{Bin: cfg.IsoInfoBin},
		Tools:          toolReg,
		LibraryRoot:    cfg.LibraryMovies,
		WorkRoot:       filepath.Join(cfg.DataPath, "work"),
		URLsForTrigger: urlsForTrigger,
		ShouldEject:    shouldEjectOnFinish,
	}))

	// Spool + Compute pool back the splittable rip→transcode flow. The
	// pool size reads from compute.concurrent_encodes (default 1); the
	// orchestrator type-asserts handlers to pipelines.SplittableHandler
	// and routes through Compute when both Spool and Compute are wired.
	// Audio CD + DATA stay on the monolithic Run path because their
	// handlers don't implement SplittableHandler.
	spoolStore, err := spool.New(filepath.Join(dataPath, "spool"))
	if err != nil {
		slog.Error("spool.New", "err", err)
		os.Exit(1)
	}
	encConc := readIntSetting(store, "compute.concurrent_encodes", 1)
	compute := jobs.NewCompute(jobs.ComputeConfig{
		Store:          store,
		Broadcaster:    bc,
		Pipelines:      pipeReg,
		Spool:          spoolStore,
		Concurrency:    encConc,
		Tools:          toolReg,
		URLsForTrigger: urlsForTrigger,
	})
	defer compute.Close()

	// Orchestrator drives jobs through the pipeline.
	orch := jobs.NewOrchestrator(jobs.OrchestratorConfig{
		Store:       store,
		Broadcaster: bc,
		Pipelines:   pipeReg,
		Compute:     compute,
		Spool:       spoolStore,
		// Re-read spool.cap_bytes on each backpressure check so a
		// setting update from the UI takes effect without a restart.
		CapBytesFunc: func() int64 {
			return int64(readIntSetting(store, "spool.cap_bytes", 100*1024*1024*1024))
		},
		Tools:          toolReg,
		URLsForTrigger: urlsForTrigger,
	})
	defer orch.Close()

	// GC at startup: a daemon crash mid-pipeline leaves spool dirs behind.
	// Runs after NewOrchestrator (which called MarkInterruptedJobs), so
	// crashed jobs are already `interrupted` and ActiveSpoolReferences
	// reflects the true keep-set: transcode-referenced spools survive for
	// the retry-transcode flow, while orphan crashed-rip dirs (no row
	// references them) are reclaimed on this boot rather than the next.
	if n, gcErr := spoolStore.GC(context.Background(), store); gcErr != nil {
		slog.Warn("spool GC at startup", "err", gcErr)
	} else if n > 0 {
		slog.Info("spool GC at startup", "removed", n)
	}

	// HTTP API.
	apiH := &api.Handlers{
		Store:         store,
		Broadcaster:   bc,
		Orchestrator:  orch,
		Compute:       compute,
		Spool:         spoolStore,
		Pipelines:     pipeReg,
		Classifier:    classifier,
		TMDB:          tmdbClient,
		MusicBrainz:   mbClient,
		IGDB:          igdbClient,
		BootCodeIndex: bootCodeIndex,
		Token:         cfg.Token,
		// ActiveSampler is started after the orchestrator's ctx is built (below).
		Apprise:              appriseTool,
		Settings:             cfg,
		NVENCAvailable:       nvencAvailable,
		Integrations:         integReg,
		IntegrationEnvLoader: envForIntegration,
		Ejector: func(ctx context.Context, devPath string) error {
			return ejectTool.Run(ctx, []string{devPath}, nil, "", tools.NopSink{})
		},
		TrayCloser: func(ctx context.Context, devPath string) error {
			return ejectTool.Run(ctx, []string{"-t", devPath}, nil, "", tools.NopSink{})
		},
	}

	embedFS, err := embed.FS()
	if err != nil {
		slog.Error("embed FS", "err", err)
		os.Exit(1)
	}
	staticHandler := api.StaticHandler(embedFS)

	router := api.NewRouter(apiH, staticHandler)
	server := api.NewServer(cfg.Addr, router)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Active-jobs sampler maintains a 24-hour ring of active-job counts
	// in memory for the ACTIVE JOBS widget's delta + sparkline. Restart
	// loses history; acceptable for a dashboard widget.
	apiH.ActiveSampler = api.NewActiveJobsSampler(store)
	apiH.ActiveSampler.Start(ctx)

	// Library sizer walks the configured roots on a 30-min timer and
	// caches per-library on-disk sizes for the Settings → Libraries view.
	// The first walk runs async on boot; the snapshot reports unmeasured
	// until it lands.
	apiH.LibrarySizer = api.NewLibrarySizer(cfg)
	apiH.LibrarySizer.Start(ctx)

	// disc-flow: listen for udev optical-media-change events and run
	// classify → identify → persist.
	df := &discFlow{
		store:      store,
		bc:         bc,
		classifier: classifier,
		pipelines:  pipeReg,
		igdb:       igdbClient,
		// 120s is enough for cd-info + fs.List + sysCNF.Probe on a slow
		// drive where each probe individually takes 20-25s (observed on
		// the ASUS SDRW-08D2S-U with a chilled PSX disc). 30s was too
		// tight, 60s also clipped some PSX discs that needed the cd-info
		// retry budget plus a full fs+sysCNF probe pass.
		identifyDur: 120 * time.Second,
		eject: func(ctx context.Context, devPath string) error {
			return ejectTool.Run(ctx, []string{devPath}, nil, "", tools.NopSink{})
		},
	}
	apiH.Reclassify = df.HandleManual
	go func() {
		if err := drive.Watch(ctx, df.handle); err != nil {
			slog.Error("udev watcher exited", "err", err)
		}
	}()

	sweeper := &state.Sweeper{
		Store:    store,
		Settings: store, // *Store satisfies SettingsReader via GetBool/GetInt
		Now:      time.Now,
		Logger:   slog.Default(),
	}
	sweeper.Start(ctx)

	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server failed", "err", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		slog.Info("shutdown requested")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("graceful shutdown failed", "err", err)
			os.Exit(1)
		}
	}
}

func firstEnv(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// readIntSetting reads an integer-valued setting from the store and
// returns def on any error (missing key, parse error, store unreachable).
// Used at startup for values like compute.concurrent_encodes where a
// missing row should never block daemon boot.
func readIntSetting(store *state.Store, key string, def int) int {
	v, err := store.GetSetting(context.Background(), key)
	if err != nil || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}
