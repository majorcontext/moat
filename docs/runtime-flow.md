# Runtime Flow: Interactive vs Non-Interactive

Here's the summary:

  Created docs/runtime-flow.md with 7 mermaid diagrams tracing both paths from CLI entry through container lifecycle:

  1. Entry points → ExecuteRun()
  2. The interactive/non-interactive fork
  3. manager.Create() (shared heavyweight setup)
  4. Non-interactive: manager.Start() → background
  5. Interactive: RunInteractiveAttached() → event loop
  6. manager.StartAttached() internals
  7. monitorContainerExit() cleanup

  7 simplification opportunities identified, ranked by impact/effort:
  ┌──────────┬──────────────────────────────────────────────┬────────────────────────────────────────────────────────────────────────────┐
  │ Priority │                     What                     │                                    Why                                     │
  ├──────────┼──────────────────────────────────────────────┼────────────────────────────────────────────────────────────────────────────┤
  │ 1        │ Remove TTY field from ExecOptions            │ Always equals Interactive — dead weight                                    │
  ├──────────┼──────────────────────────────────────────────┼────────────────────────────────────────────────────────────────────────────┤
  │ 2        │ Remove streamLogs from non-interactive Start │ Users use moat logs -f now; goroutine lifecycle is unclear                 │
  ├──────────┼──────────────────────────────────────────────┼────────────────────────────────────────────────────────────────────────────┤
  │ 3        │ Extract postStart() helper                   │ Start() and StartAttached() share ~60% of code                             │
  ├──────────┼──────────────────────────────────────────────┼────────────────────────────────────────────────────────────────────────────┤
  │ 4        │ Simplify interactive event loop              │ Only one escape action now; the channel dance is overkill                  │
  ├──────────┼──────────────────────────────────────────────┼────────────────────────────────────────────────────────────────────────────┤
  │ 5        │ Extract idempotent cleanupRun()              │ Cleanup is duplicated across 3 paths with inconsistent guards              │
  ├──────────┼──────────────────────────────────────────────┼────────────────────────────────────────────────────────────────────────────┤
  │ 6        │ ProviderRunner helper                        │ claude/codex/gemini CLIs are ~200 lines each of near-identical boilerplate │
  ├──────────┼──────────────────────────────────────────────┼────────────────────────────────────────────────────────────────────────────┤
  │ 7        │ Skip: TTY trace builder                      │ Future-proofing, not needed now                                            │
  └──────────┴──────────────────────────────────────────────┴────────────────────────────────────────────────────────────────────────────┘
  Want me to proceed with any of these?

Current state after the attach/detach simplification.

## Entry Points

All commands converge on `ExecuteRun()`:

```mermaid
flowchart TD
    A["moat run"]
    B["moat claude"]
    C["moat codex"]
    D["moat gemini"]
    E["moat wt"]

    A -->|"parse args, load config,<br/>resolve interactive mode"| F["ExecuteRun()"]
    B -->|"interactive = (no -p flag)"| F
    C -->|"interactive = (no -p flag)"| F
    D -->|"interactive = (no -p flag)"| F
    E -->|"interactive = config.Interactive"| F
```

Each entry point builds an `ExecOptions` struct with `Interactive` and `TTY` set identically (both true or both false), then calls `ExecuteRun()`.

## ExecuteRun: The Fork

```mermaid
flowchart TD
    F["ExecuteRun()"]
    F --> G["Set runtime env"]
    G --> H["NewManagerWithOptions()"]
    H --> I["manager.Create(ctx, opts)"]

    I --> WT{"worktree<br/>metadata?"}
    WT -->|yes| WTS["Save worktree metadata"]
    WT -->|no| CB
    WTS --> CB

    CB{"OnRunCreated<br/>callback?"}
    CB -->|yes| CBS["Call callback"]
    CB -->|no| MODE
    CBS --> MODE

    MODE{"opts.Interactive?"}
    MODE -->|yes| INT["RunInteractiveAttached()"]
    MODE -->|no| NONINT["manager.Start()"]

    NONINT --> PORTS{"ports?"}
    PORTS -->|yes| PRINTP["Print endpoints"]
    PORTS -->|no| BG
    PRINTP --> BG["Print background instructions<br/>(moat logs, moat stop)"]
    BG --> RET["return"]

    INT --> DONE["Blocks until process exits<br/>or Ctrl-/ k"]
```

## manager.Create(): Shared Setup (Both Modes)

This is the heavyweight function (~500 lines). Both modes go through the same path.

```mermaid
flowchart TD
    C["manager.Create()"]
    C --> NAME["Resolve name<br/>(flag > config > random)"]
    NAME --> VGRANT["Validate grants<br/>(credential store check)"]
    VGRANT --> RUN["Create Run struct<br/>(ID, state=Created)"]

    RUN --> MOUNTS["Build mounts<br/>(workspace + config + volumes + worktree .git)"]

    MOUNTS --> PROXY{"needs proxy?<br/>(grants or strict network)"}
    PROXY -->|yes| DAEMON["EnsureRunning(daemon)"]
    DAEMON --> CREDS["Load credentials per grant"]
    CREDS --> RUNCTX["Build RunContext<br/>(headers, network rules, MCP)"]
    RUNCTX --> REGISTER["daemon.RegisterRun()"]
    REGISTER --> PROXYENV["Set proxy env vars<br/>(HTTP_PROXY, CA cert mount)"]
    PROXY -->|no| NOPROXY["No proxy setup"]

    PROXYENV --> SSH
    NOPROXY --> SSH

    SSH{"ssh grant?"}
    SSH -->|yes| SSHA["Start SSH agent server<br/>(mount socket)"]
    SSH -->|no| AGENTCFG

    SSHA --> AGENTCFG

    AGENTCFG{"agent provider?<br/>(claude/codex/gemini)"}
    AGENTCFG -->|yes| PCFG["Provider.ContainerConfig()<br/>(config files, env, mounts)"]
    AGENTCFG -->|no| IMG

    PCFG --> IMG["image.Resolve()<br/>(deps → base image)"]
    IMG --> BUILD{"image exists?"}
    BUILD -->|no| BUILDIMG["Build image<br/>(Dockerfile generation + build)"]
    BUILD -->|yes| SNAP
    BUILDIMG --> SNAP

    SNAP{"snapshots<br/>configured?"}
    SNAP -->|yes| SNAPENG["Create snapshot engine"]
    SNAP -->|no| CONT

    SNAPENG --> CONT
    CONT --> LANGSERV{"language<br/>servers?"}
    LANGSERV -->|yes| LS["Configure language server<br/>mounts and env"]
    LANGSERV -->|no| SVC
    LS --> SVC

    SVC{"services?<br/>(postgres, redis, etc)"}
    SVC -->|yes| SVCSTART["Create network<br/>Start service containers<br/>Wait for readiness"]
    SVC -->|no| DEPS
    SVCSTART --> DEPS

    DEPS{"build deps?<br/>(BuildKit)"}
    DEPS -->|yes| BK["Start BuildKit sidecar"]
    DEPS -->|no| AUDIT
    BK --> AUDIT

    AUDIT["Create audit store"]
    AUDIT --> CCFG["Build container.Config"]
    CCFG --> CREATE["runtime.CreateContainer()"]
    CREATE --> FW{"strict<br/>network?"}
    FW -->|yes| FWSET["Record firewall settings"]
    FW -->|no| SAVE
    FWSET --> SAVE
    SAVE["SaveMetadata()"]
    SAVE --> RRET["return Run"]
```

## Non-Interactive Path: manager.Start()

```mermaid
flowchart TD
    S["manager.Start()"]
    S --> SS["state = Starting"]
    SS --> LOG["Set log context"]
    LOG --> SC["runtime.StartContainer()"]
    SC --> FW{"firewall<br/>enabled?"}
    FW -->|yes| FWS["runtime.SetupFirewall()"]
    FW -->|no| PB
    FWS --> PB

    PB{"ports?"}
    PB -->|yes| PBG["GetPortBindings()<br/>Register routes"]
    PB -->|no| RUN
    PBG --> RUN

    RUN["state = Running"]
    RUN --> SAVE["SaveMetadata()"]
    SAVE --> SNAP{"snapshot<br/>engine?"}
    SNAP -->|yes| SNPC["Create pre-run snapshot"]
    SNAP -->|no| STREAM
    SNPC --> STREAM

    STREAM["go streamLogs()"]
    STREAM --> MON["go monitorContainerExit()"]
    MON --> RET["return"]
```

After `Start()` returns, `ExecuteRun()` prints the background instructions and the CLI exits. The container runs independently. `monitorContainerExit()` runs in a background goroutine that captures logs, runs provider hooks, and cleans up when the container eventually exits.

## Interactive Path: RunInteractiveAttached()

```mermaid
flowchart TD
    R["RunInteractiveAttached()"]
    R --> HELP["Print escape help<br/>(Ctrl-/ k)"]
    HELP --> TRACE{"tty-trace<br/>path?"}
    TRACE -->|yes| TT["Setup TTY tracer"]
    TRACE -->|no| RAW
    TT --> RAW

    RAW{"stdin is<br/>terminal?"}
    RAW -->|yes| RAWM["EnableRawMode()"]
    RAW -->|no| SB
    RAWM --> SB

    SB{"stdout is<br/>terminal?"}
    SB -->|yes| SBS["Setup status bar<br/>(scroll region, grants)"]
    SB -->|no| WRAP
    SBS --> WRAP

    WRAP["Wrap stdin: EscapeProxy"]
    WRAP --> EHINTS{"status<br/>writer?"}
    EHINTS -->|yes| EH["SetupEscapeHints()"]
    EHINTS -->|no| TWRAP
    EH --> TWRAP

    TWRAP{"tracer?"}
    TWRAP -->|yes| TW["Wrap stdin/stdout<br/>with RecordingReader/Writer"]
    TWRAP -->|no| SIG
    TW --> SIG

    SIG["signal.Notify<br/>(SIGINT, SIGTERM, SIGWINCH)"]

    SIG --> SA["go manager.StartAttached()<br/>(blocks until container exits)"]
    SA --> RESIZE["go sleep(200ms) then<br/>ResizeTTY()"]

    RESIZE --> LOOP["Event loop"]

    LOOP --> |SIGWINCH| WINCH["Resize status bar + TTY"]
    WINCH --> LOOP

    LOOP --> |SIGTERM| TERM["Stop run"]
    TERM --> EXIT["return"]

    LOOP --> |SIGINT| FWDI["Forwarded to container<br/>via TTY"]
    FWDI --> LOOP

    LOOP --> |EscapeStop| STOP["Cancel attach<br/>manager.Stop()"]
    STOP --> EXIT

    LOOP --> |attachDone err| ERR{"error?"}
    ERR -->|yes| ERRET["return error"]
    ERR -->|no| COMP["Print completed"]
    COMP --> EXIT
```

## manager.StartAttached()

```mermaid
flowchart TD
    SA["manager.StartAttached()"]
    SA --> SS["state = Starting"]
    SS --> LOG["Set log context"]
    LOG --> TTY{"stdin is terminal?"}
    TTY -->|yes| USTY["useTTY = true"]
    TTY -->|no| USTN["useTTY = false"]

    USTY --> TEE
    USTN --> TEE

    TEE{"interactive +<br/>has store?"}
    TEE -->|yes| TEEW["Tee stdout/stderr<br/>to logBuffer"]
    TEE -->|no| AOPTS
    TEEW --> AOPTS

    AOPTS["Build AttachOptions<br/>(stdin, stdout, stderr, TTY,<br/>initial terminal size)"]
    AOPTS --> GORTN["go runtime.StartAttached()"]
    GORTN --> DELAY["sleep(100ms)"]
    DELAY --> RUN["state = Running"]
    RUN --> PB{"ports?"}
    PB -->|yes| PBG["GetPortBindings()<br/>Register routes"]
    PB -->|no| SAVE
    PBG --> SAVE
    SAVE["SaveMetadata()"]
    SAVE --> FW{"firewall?"}
    FW -->|yes| FWS["SetupFirewall()"]
    FW -->|no| WAIT
    FWS --> WAIT

    WAIT["await attachDone"]
    WAIT --> LOGS{"Apple +<br/>interactive?"}
    LOGS -->|yes| WLOGS["Write logBuffer<br/>to logs.jsonl"]
    LOGS -->|no| CAP
    WLOGS --> CAP

    CAP["captureLogs()"]
    CAP --> HOOKS["runProviderStoppedHooks()"]
    HOOKS --> SMETA["SaveMetadata()"]
    SMETA --> RET["return attachErr"]
```

## Cleanup: monitorContainerExit()

Runs as a background goroutine for **non-interactive** runs (started by `manager.Start()`). For **interactive** runs, cleanup is done inline in `manager.StartAttached()`.

```mermaid
flowchart TD
    M["monitorContainerExit()"]
    M --> WAIT["runtime.WaitContainer()<br/>(blocks)"]
    WAIT --> CTX{"context<br/>canceled?"}
    CTX -->|yes| BAIL["return (container still running,<br/>next Manager instance picks it up)"]
    CTX -->|no| LOGS["captureLogs()"]

    LOGS --> HOOKS["runProviderStoppedHooks()"]
    HOOKS --> CLOSE["close(exitCh)"]
    CLOSE --> STATE{"exit code?"}
    STATE -->|0| STOPPED["state = Stopped"]
    STATE -->|non-zero| FAILED["state = Failed"]
    STOPPED --> SAVE
    FAILED --> SAVE

    SAVE["SaveMetadata()"]
    SAVE --> CLEANUP["cleanupResources()<br/>(sync.Once guarded)"]
```

---

## Completed Simplifications

The following simplifications were identified during analysis and have been implemented:

### 1. Removed `TTY` field from ExecOptions ✅
`ExecOptions.TTY` was always set identically to `Interactive`. Removed the field — TTY is now determined at runtime via `term.IsTerminal(os.Stdin)` in `manager.StartAttached()`.

### 2. Removed `streamLogs` from non-interactive Start ✅
`StreamLogs` was never `true` in production. Non-interactive runs tell users to use `moat logs -f`. Removed the `streamLogs` method and its `stdcopy` import.

### 3. Extracted postStart helpers ✅
Extracted `setLogContext()`, `setupPortBindings()`, and `setupFirewall()` from the duplicated code in `Start()` and `StartAttached()`. Reduced ~80 lines of duplication.

### 4. Simplified interactive event loop ✅
With only one escape action (`EscapeStop`), eliminated the `escapeCh` channel. Escape errors now flow through `attachDone` and are handled inline. The attach goroutine is a one-liner.

### 5. Extracted idempotent `cleanupResources()` ✅
Consolidated resource cleanup from `Stop()`, `Wait()`, `monitorContainerExit()`, and `Destroy()` into a single method guarded by `sync.Once`. Reduced ~300 lines of duplicated cleanup code.

### 6. Extracted `ProviderRunner` helper ✅
`moat claude`, `moat codex`, and `moat gemini` now use a shared `RunProvider()` helper. Each provider is ~30-50 lines of declarative config instead of ~170 lines of boilerplate. Provider-specific logic is isolated in `BuildCommand` and `ConfigureAgent` callbacks.

### 7. TTY trace builder pattern — Skipped
The conditional wrapping is clear as-is. Not needed until more layers are added.

The duplication is significant (~200 lines each with minor variations).

**Opportunity:** Extract a `ProviderRunner` helper that takes a provider-specific config (dependencies, command builder, flag names) and handles the boilerplate. Each provider CLI becomes ~50 lines of config + flag registration.

### 7. Non-interactive Start() still streams logs to stdout

`manager.Start()` calls `go m.streamLogs()` which copies container logs to stdout. But `ExecuteRun()` returns immediately after Start() for non-interactive mode, and the manager is deferred-closed. The `streamLogs` goroutine's lifecycle is unclear — it may be killed when the manager closes.

**Opportunity:** Since non-interactive runs print "use moat logs -f", consider removing the `streamLogs` call entirely. The user explicitly monitors via `moat logs`. This simplifies the non-interactive path and eliminates the goroutine lifecycle question.

### Priority ranking

| # | Opportunity | Impact | Effort |
|---|------------|--------|--------|
| 5 | Remove `TTY` field (always = Interactive) | Low risk, cleaner API | Small |
| 7 | Remove streamLogs from non-interactive Start | Simpler lifecycle | Small |
| 1 | Extract postStart() helper | Less duplication in manager | Medium |
| 3 | Simplify interactive event loop | Fewer channels/goroutines | Medium |
| 2 | Idempotent cleanupRun() | Correctness + less duplication | Medium |
| 6 | ProviderRunner helper | Major dedup across 3 files | Large |
| 4 | TTY trace builder pattern | Future-proofing only | Skip |
