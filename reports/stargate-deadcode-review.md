# Stargate dead-code review

**Target:** `~/sandbox/starlight/stargate`  
**Date:** 2026-08-09  
**Tool:** `dcd` (`github.com/eric/deadcodedetector`)  
**Reviewer note:** this is a static-analysis report for human review. Do not delete from the raw counts. About half of the frontend hits are tool noise.

## Verdict

The backend has a real unused surface: leftover constructors, lifecycle methods (`Stop` / `IsRunning` / `Close`), unused HTTP/MCP helpers, unused DTO types in `models/`, and whole repository types (`ProposalsRepository`, `TasksRepository`, `SchemaManager`) that nothing constructs.

The frontend has a smaller set of likely-dead React modules, plus a pile of unused `API_BASE` imports and a dead `App.css` layout. Most of the 386 CSS hits are Tailwind-style utilities in `src/index.css` and should be ignored.

**Recommended action:** review and land in three small PRs (models/DTOs, unused Go helpers, orphan frontend modules + App.css). Do not attempt a single 667-line cleanup.

## How it was run

```text
# backend — unused-reference only
dcd -lang go -reachable=false ~/sandbox/starlight/stargate/backend

# frontend
dcd -lang js,css ~/sandbox/starlight/stargate/frontend
```

Whole-program Go reachability (RTA / SSA) was started and killed after ~10 minutes at 100% CPU, 125 MB RSS, no output. The backend pulls btcd, libp2p, IPFS, pgx, gorm. Loading all dependency syntax and building SSA is not practical on this tree with the current detector. Hence this report is **name-reference unused**, not “unreachable from `main`”.

That distinction matters: a function that is only called from other unused functions still counts as used here. A method required only via an interface in another module, or via `http.Handler` registration that the checker did not attribute, can be a false positive.

Raw tool output (kept on the scan host):

- `/tmp/dcd-stargate/go.txt` — 210 findings
- `/tmp/dcd-stargate/js-css.txt` — 457 findings (js=71, css=386)

## Counts

| Area | Findings | Suggested to review first | Ignore / false positive |
|---|---:|---:|---|
| Go unused method | 98 | ~70 production methods | 17 `_test.go` mock methods; some GORM `TableName` |
| Go unused function | 69 | constructors + handlers + helpers | test helpers (`setupTestPGStore`, `hasSeparators`) |
| Go unused type | 20 | `models.*` DTOs, unused storage types | maybe keep if they are the JSON schema |
| Go unused const | 23 | unused status / MCP error codes | keep if they document the protocol |
| JS unused file | 22 | 4 orphan components | configs, tests, Playwright, re-export chain |
| JS unused import | 38 | leftover `API_BASE`, lucide icons, `useState` | `React` (often real), `CONTENT_BASE` in templates |
| JS unused var | 10 | none of the `export default Foo` hits | all 10 are default-export locals |
| JS unused export | 1 | `saveDB` export only | function body is live |
| CSS unused selector / keyframes | 386 | `App.css` (19) + custom `index.css` clusters | ~300 Tailwind utilities |

**Headline: 667 raw / roughly 120–160 worth a human pass.**

---

## Proposed review order

### P0 — orphan frontend modules (low risk, easy to verify)

Nothing under `frontend/src` imports these except their own file:

| File | Why it looks dead |
|---|---|
| `frontend/src/StegoAnalysisViewer.jsx` | default export, no importers |
| `frontend/src/components/Common/ConfidenceIndicator.jsx` | same |
| `frontend/src/components/Review/SubmissionReview.jsx` | same |
| `frontend/src/components/Inscription/InscriptionContentTab.jsx` | same |

Check git history before delete (last caller, any dynamic `import()` I missed). If they are mid-migration, add `// dcd:ignore` rather than remove.

### P1 — Go unused API / storage that is not wired

These are the ones I would actually open in an editor:

**Unused constructors / facades**

- `bitcoin.NewBitcoinAPI`, `NewBlockMonitorWithStorage`, `NewBlockMonitorWithAPI`
- `app/smart_contract/middleware.NewAuthMiddleware` + `AuthMiddleware.RequireAPIKey` + `CORS`
- `starlight.NewProxyScanner`
- `storage/auth.NewAIChallengeStore`
- `storage/smart_contract.NewProposalsRepository` and the whole `ProposalsRepository.{List,Get,Create,UpdateMetadata}` set
- `storage/smart_contract.NewTasksRepository` and `{List,Upsert,UpdateProof,Status}`
- `storage/smart_contract.NewSchemaManager` + `Initialize` / `SeedFixtures`

If production uses SQL store directly and these repos were an abandoned layer, they can go. If they are the intended next storage API, they stay and we should add a test or a call site so they stop looking dead.

**Unused lifecycle**

- `agents.Orchestrator.Stop` / `IsRunning`
- `bitcoin.BlockMonitor.Stop` / `IsRunning`
- `container.Container.Close`
- `core/smart_contract.EscortService.Stop`

These are the usual “we start it, we never shut it down” leftovers. Confirm nothing relies on them for tests or graceful shutdown, then either wire them from `main`/signal handling or delete.

**Unused HTTP / MCP surface**

- `handlers.APIKeyHandler.HandleRegister`
- `handlers.BlockHandler.HandleGetBlocks`
- `handlers.SmartContractHandler.HandleCreateContract` / `HandleGetContract`
- `app/smart_contract.Server.handleGetContractReworkRequests`
- `mcp.HTTPMCPServer.authWrap`, `statusFromError`, `verifyBTCSignature`
- `middleware.ContentType`, `middleware.RateLimit`

Handlers with no `Handle*` call and no router registration are the highest-confidence Go deletes. Confirm against the router table (`api/surfaces.go`, MCP tool map) before removing.

**Unused DTO / type pile in `models/models.go`**

```
SmartContractImage, ContractMetadata, Block, HealthResponse,
SuccessResponse, QRCodeRequest, BlockImagesRequest, BlockImagesResponse,
SearchRequest, SmartContractsResponse
```

plus `NewErrorResponseWithHint`, `NewSuccessResponseWithMeta`.

If the live handlers now use types from `core/` or ad-hoc maps, this file is leftover OpenAPI-era types. If Swagger still documents them, keep and ignore.

### P2 — cleanup, not architecture

- Unused `API_BASE` imports once the template-string false positive is filtered (see JS section).
- Unused lucide icons and `useState` in `BlockCard.jsx`.
- Entire `frontend/src/App.css` old layout (file is still imported from `index.jsx`).
- Unused status constants in `core/smart_contract/types.go` and `dispute_resolution.go` if the live code uses string literals instead.

---

## Go findings (210)

### By package

| Package | Count | Reading |
|---|---:|---|
| `storage/` | 50 | Dead repos, schema manager, rate-limiter/security helpers, unused GORM `TableName` |
| `bitcoin/` | 43 | Extra constructors, unused monitor methods, 14 `mockChain` test methods |
| `core/` | 37 | Dispute/escrow/script methods + unused status consts |
| `app/` | 24 | Ingest helpers, unused middleware, unused `Server` helpers |
| `handlers/` | 16 | Unused `Handle*` + local helpers |
| `mcp/` | 12 | Error helpers and unused HTTP methods |
| `models/` | 12 | Unused request/response types |
| `api/` | 4 | `RegisterAliases`, `isAVIF` |
| other | 12 | agents, container, starlight, stego, services, middleware |

### Test-only (skip in the first PR)

All `mockChain.*` on `bitcoin/chain_backend_test.go` (14 methods). These implement `ChainBackend`; the detector did not keep them because the interface check did not attach the test mock. Same class: `handlers/security_test.go hasSeparators`, `storage/gormdb/db_test.go autoMigrateRow.TableName`, `storage/smart_contract/pg_store_validation_test.go setupTestPGStore`.

**17 findings, do not delete.**

### GORM `TableName` methods

`APIKeyRow.TableName`, `gormClaimRow.TableName`, `gormTaskRow.TableName`, `BlockScanRow.TableName` look unused by identifier. Gorm calls them via interface. **Keep.** `moderncSQLiteDialector.SavePoint` / `RollbackTo` are likely `gorm.Dialector` methods. **Keep.**

### Full Go list

See [Appendix A](#appendix-a--go-findings-210). Each line is `file:line:col: kind name` as emitted by `dcd`.

---

## JavaScript findings (71)

### Confirmed live, tool is wrong

| Finding | Why false |
|---|---|
| `export default Foo` reported as unused var (`BlockCard`, `OpenContractsView`, `AppHeader`, `CopyButton`, `SafeQrCodeCanvas`, `InscriptionCard`, `InscriptionModal`, `DeliverablesReview`) | local name is the default export. Detector does not count `export default Ident` as a use. |
| `App.jsx` `const InscriptionModal = lazy(...)` unused var | used as `<InscriptionModal` around line 1216. JSX use missed (large file / lazy binding). |
| `CONTENT_BASE` unused import in `App.jsx` | used inside template literals (`\`${CONTENT_BASE}/uploads/...\``). Scanner does not pull idents out of templates. |
| `WishChatModal.jsx`, `wishChatBot.js` unused files | live via `InscribeModal.jsx`: `export { default } from './WishChatModal'`. Re-export-from is not followed as a module edge. `App.jsx` lazy-loads `InscribeModal`. |
| `vite.config.js`, `eslint.config.cjs`, `postcss.config.js`, `scripts/generate-sitemap.js` | tool entries, not imported by the app graph. `package.json` scripts call them. |
| Playwright `tests/*.spec.js`, `src/setupTests.js`, `*.test.jsx` / `*.test.js` | test runner entries. |

### Likely real

**Orphan components** (P0 above).

**Unused imports that look real**

| Location | Symbol | Note |
|---|---|---|
| `src/App.jsx:27` | `API_BASE` | imported next to `CONTENT_BASE`; only `CONTENT_BASE` appears in the file |
| `src/components/Block/BlockCard.jsx:1` | `useState` | imported, not called |
| `src/components/Common/AppHeader.jsx:2` | `Menu` | lucide icon unused |
| `src/components/Inscription/InscriptionModal.jsx:7` | `DeliverablesReview` | import with no JSX use |
| `src/components/Inscription/InscriptionModal.jsx:9` | `shouldShowProposalAction` | import with no call |
| `src/components/Review/DeliverablesReview.jsx:2` | `Clock`, `Code`, `FileText` | lucide unused |
| `src/components/Review/DeliverablesReview.jsx:4` | `getSubmissionTimestamp` | unused helper import |

`React` default imports (many files) are unused under the automatic JSX runtime (React 17+ / Vite). Safe to drop; eslint would say the same. Not “dead product code”.

`API_BASE` / `CONTENT_BASE` in hooks (`useBlocks.js`, `useContracts.js`, `useInscriptions.js`, `useInscriptionModalState.js`, `utils/api.js`, `DiscoverPage.jsx`, `OpenContractsView.jsx`, `McpDocsPage.jsx`, `AuthContext.jsx`) need a file-by-file check. Any use that is only inside `` `${API_BASE}...` `` is a false unused-import. Spot-check `App.jsx`: `CONTENT_BASE` is that case; `API_BASE` is not.

**Unused export**

- `src/utils/db.js:96` `saveDB` — called from other functions in the same file, never imported elsewhere. Drop the `export` keyword, keep the function.

### Full JS list

See [Appendix B](#appendix-b--javascript-findings-71).

---

## CSS findings (386)

### `src/App.css` — 19 selectors, all unused

File is still imported from `src/index.jsx`. Classes are the old block/inscription layout (`.main-content`, `.blocks-container`, `.block-item`, `.inscriptions-grid`, `.inscription-item`, `.view-link`, `.contract-display`). Live UI has moved to QuantumCSS / utility classes in `index.css`.

**Review as one unit:** delete the unused rules, or delete the file and the `import './App.css'` if nothing remains.

### Custom `index.css` clusters that match dead UI

These are not Tailwind utilities. They line up with unused or half-migrated UI:

- `@keyframes blockSlideIn`, `glowPulse`, `nebula-drift`; `.block-slide-in`, `.ani-nebula`
- `.form-input` / `.form-select` (and theme variants)
- `.wish-chat-*` — consistent with `WishChatModal` looking unused *to the tool*. **Do not delete the wish-chat CSS until the re-export false positive is resolved.** `InscribeModal` re-exports `WishChatModal`, so this CSS is probably live.
- `.contract-success-btn*`, `.modal-form-content …`, `.contract-submitted-title`
- `.deliverables-list-review-btn*`, `.deliverables-proof-*`, `.markdown-content .md-*`

`.markdown-content` may be live via `MarkdownContent.jsx` (class name applied in JS as a string). If the component sets `className="markdown-content"`, the tool should have seen the string. If it did not, that is a string-harvest miss — check before deleting.

### Tailwind / generated utilities — ignore

`src/index.css` from ~line 7200 down is almost all `.md:grid-cols-*`, `.lg:px-8`, `.dark:bg-gray-900`, `.hover:bg-white/10:hover`, etc. Hundreds of hits. This is how a dumped utility stylesheet looks to a class-name scanner: the HTML uses a subset, the file contains the kit.

**Do not hand-delete these.** If you want them gone, regenerate the CSS from QuantumCSS / the safelist, do not edit the dump.

---

## What the tool cannot see (read before arguing with a finding)

1. **Go:** no RTA on this repo. Call-graph-dead-but-referenced functions are not reported. Interface methods on mocks and GORM hooks are over-reported.
2. **Go:** `http.HandleFunc(path, h.HandleX)` uses `HandleX`. If the router takes a method value, it should count. If it uses reflection or a generated table of names, it will not.
3. **JS:** `export default Name` and `export { default } from './x'` are not modeled correctly. Template-literal identifiers are not uses.
4. **JS:** Vite/Playwright/eslint config files are not entries unless listed in `package.json` `main`/`module`/`bin` or `-entry`.
5. **CSS:** a class present as any string in JS/HTML counts as used. Dynamic `className={cond ? 'a' : 'b'}` is fine; `className={cls}` where `cls` is computed without the literal is a miss (false unused).

---

## Suggested PR split

| PR | Scope | Approx. size | Risk |
|---|---|---|---|
| 1 | Delete or ignore the 4 orphan React files (+ their tests if any) | small | low |
| 2 | Drop unused `API_BASE` / icon / `useState` imports after a template-string pass | small | low |
| 3 | Strip or remove `App.css` old layout | small | low (visual check) |
| 4 | Go: unused `models` DTOs + unused constructors that have no router/main call | medium | medium — confirm swagger / MCP tool list |
| 5 | Go: unused `ProposalsRepository` / `TasksRepository` / `SchemaManager` if truly abandoned | medium | medium — storage migration risk |
| 6 | Go: wire or delete `Stop`/`Close` lifecycle | small | ops — shutdown behavior |

I would not open a PR that is “delete everything in appendix A”.

---

## Repro / refresh

From the detector repo:

```bash
go build -o dcd ./cmd/dcd
./dcd -lang go -reachable=false -format text ~/sandbox/starlight/stargate/backend
./dcd -lang js,css -format text ~/sandbox/starlight/stargate/frontend
./dcd -format json ~/sandbox/starlight/stargate/backend   # machine-readable, Go only
```

Suppress a kept symbol:

```go
// dcd:ignore
func NewBitcoinAPI(...) *BitcoinAPI { ... }
```

```js
// dcd:ignore
export function saveDB() { ... }
```

```css
/* dcd:ignore */
.legacy-modal { }
```

---

## Appendix A — Go findings (210)

```
agents/orchestrator.go:70:24: unused method Orchestrator.Stop
agents/orchestrator.go:153:24: unused method Orchestrator.IsRunning
api/data_api_content.go:584:6: unused function isAVIF
api/surfaces.go:115:6: unused function RegisterAliases
api/surfaces.go:122:6: unused function RegisterAliasHandlers
api/surfaces.go:132:6: unused function LogDeprecationOnce
app/smart_contract/funding_provider_blockcypher.go:40:6: unused type blockcypherMerkleRoot
app/smart_contract/ipfs_ingest_sync.go:384:6: unused function fetchStegoPayload
app/smart_contract/ipfs_ingest_sync.go:825:6: unused function resolveProposalIDsForIngestUpdate
app/smart_contract/ipfs_ingest_sync.go:969:6: unused function multibaseEncodeString
app/smart_contract/ipfs_ingest_sync.go:1016:6: unused function ingestPlainStegoWish
app/smart_contract/middleware/middleware.go:31:6: unused function NewAuthMiddleware
app/smart_contract/middleware/middleware.go:36:26: unused method AuthMiddleware.RequireAPIKey
app/smart_contract/middleware/middleware.go:55:6: unused function CORS
app/smart_contract/server_contracts.go:16:6: unused function applyCreatorWallet
app/smart_contract/server_contracts.go:245:18: unused method Server.handleGetContractReworkRequests
app/smart_contract/server_proposals.go:208:6: unused function BuildProposalFromIngestion
app/smart_contract/server_psbt.go:933:18: unused method Server.ingestionFromProposalMeta
app/smart_contract/server_psbt.go:949:6: unused function looksLikeRaiseFund
app/smart_contract/server_psbt.go:957:6: unused function fundingAddressFromMeta
app/smart_contract/server_psbt.go:1011:18: unused method Server.resolveContractorPayers
app/smart_contract/server_psbt.go:1337:6: unused function contractIDFromMeta
app/smart_contract/services/errors.go:24:6: unused function Failf
app/smart_contract/services/proposal_service.go:98:27: unused method ProposalService.SetRecorder
app/smart_contract/services/proposal_service.go:101:27: unused method ProposalService.SetPublishTasks
app/smart_contract/services/proposal_service.go:106:27: unused method ProposalService.SetArchiveWish
app/smart_contract/services/submission_service.go:40:29: unused method SubmissionService.SetRecorder
app/smart_contract/stego_reconcile.go:57:6: unused function generatePayoutScript
app/smart_contract/storage.go:16:6: unused type Err
app/smart_contract/sync_pubsub.go:205:6: unused function decodePubsubPayload
bitcoin/api.go:30:6: unused function NewBitcoinAPI
bitcoin/api.go:748:24: unused method BitcoinAPI.GetBitcoinClient
bitcoin/block_monitor.go:59:7: unused const reconcileSweepInterval
bitcoin/block_monitor.go:61:7: unused const reconcileSweepBlocks
bitcoin/block_monitor.go:249:6: unused function NewBlockMonitorWithStorage
bitcoin/block_monitor.go:266:6: unused function NewBlockMonitorWithAPI
bitcoin/block_monitor.go:363:25: unused method BlockMonitor.Stop
bitcoin/block_monitor.go:379:25: unused method BlockMonitor.IsRunning
bitcoin/block_monitor.go:460:25: unused method BlockMonitor.ChainBackend
bitcoin/block_monitor_process.go:553:25: unused method BlockMonitor.parseTxOutputsFromJSON
bitcoin/block_monitor_save.go:364:25: unused method BlockMonitor.saveBlockSummary
bitcoin/block_monitor_save.go:397:25: unused method BlockMonitor.calculateTransactionSize
bitcoin/block_monitor_scan.go:101:6: unused function stripPushdataPrefixLocal
bitcoin/block_monitor_scan.go:131:6: unused function stripNonPrintablePrefixLocal
bitcoin/block_monitor_scan.go:170:25: unused method BlockMonitor.scanBlockViaAPI
bitcoin/block_monitor_scan.go:822:25: unused method BlockMonitor.cleanupUploadArtifacts
bitcoin/block_monitor_scan.go:1053:6: unused function contractIDFromIngestion
bitcoin/block_monitor_scan.go:1233:25: unused method BlockMonitor.GetBlockInscriptions
bitcoin/block_monitor_util.go:74:25: unused method BlockMonitor.persistDiscoveryContract
bitcoin/btcd_node.go:587:6: unused function ParseRPCPort
bitcoin/chain_backend_test.go:16:21: unused method mockChain.Network
bitcoin/chain_backend_test.go:17:21: unused method mockChain.Ready
bitcoin/chain_backend_test.go:18:21: unused method mockChain.Synced
bitcoin/chain_backend_test.go:19:21: unused method mockChain.GetTipHeight
bitcoin/chain_backend_test.go:20:21: unused method mockChain.GetBlockHash
bitcoin/chain_backend_test.go:21:21: unused method mockChain.GetRawBlockHex
bitcoin/chain_backend_test.go:22:21: unused method mockChain.GetRawTx
bitcoin/chain_backend_test.go:25:21: unused method mockChain.GetTxStatus
bitcoin/chain_backend_test.go:28:21: unused method mockChain.NodeStatus
bitcoin/chain_backend_test.go:31:21: unused method mockChain.Close
bitcoin/chain_backend_test.go:32:21: unused method mockChain.ListConfirmedUTXOs
bitcoin/chain_backend_test.go:33:21: unused method mockChain.FetchTx
bitcoin/chain_backend_test.go:34:21: unused method mockChain.FetchTxOutput
bitcoin/chain_backend_test.go:37:21: unused method mockChain.BroadcastTx
bitcoin/client.go:184:31: unused method BitcoinNodeClient.waitForRateLimit
bitcoin/client.go:210:31: unused method BitcoinNodeClient.GetBlockData
bitcoin/client.go:241:31: unused method BitcoinNodeClient.GetTransaction
bitcoin/commitment_sweep.go:93:6: unused function BuildRegularSweepTx
bitcoin/commitment_sweep.go:281:6: unused function buildCommitmentP2WSHScript
bitcoin/psbt_scripts.go:166:6: unused function buildHashlockP2PKHRedeemScript
bitcoin/psbt_scripts.go:211:6: unused function sumFeeShares
bitcoin/raw_block_parser.go:205:6: unused type Block
bitcoin/storage_interface.go:31:6: unused type RealtimeUpdate
container/container.go:185:21: unused method Container.Close
core/identity/identity.go:57:6: unused function ContractIDFromVisibleHash
core/identity/identity.go:102:6: unused function ExpandWishVariants
core/smart_contract/dispute_resolution.go:77:2: unused const DisputeStatusResponded
core/smart_contract/dispute_resolution.go:78:2: unused const DisputeStatusArbitrating
core/smart_contract/dispute_resolution.go:79:2: unused const DisputeStatusVoting
core/smart_contract/dispute_resolution.go:81:2: unused const DisputeStatusExpired
core/smart_contract/dispute_resolution.go:82:2: unused const DisputeStatusCanceled
core/smart_contract/dispute_resolution.go:526:30: unused method DisputeResolution.AppealDispute
core/smart_contract/dispute_resolution.go:546:30: unused method DisputeResolution.GetDisputeStatus
core/smart_contract/dispute_resolution.go:588:30: unused method DisputeResolution.GetArbitrators
core/smart_contract/escort_service.go:195:26: unused method EscortService.GetProofHealth
core/smart_contract/escort_service.go:225:26: unused method EscortService.SetCheckInterval
core/smart_contract/escort_service.go:231:26: unused method EscortService.Stop
core/smart_contract/escrow_manager.go:400:26: unused method EscrowManager.PayoutEscrow
core/smart_contract/escrow_manager.go:499:26: unused method EscrowManager.RefundEscrow
core/smart_contract/escrow_manager.go:606:26: unused method EscrowManager.GetEscrowStatus
core/smart_contract/script_interpreter.go:193:30: unused method ScriptInterpreter.ExtractScriptDetails
core/smart_contract/script_interpreter.go:380:30: unused method ScriptInterpreter.VerifySignature
core/smart_contract/script_interpreter.go:391:30: unused method ScriptInterpreter.ComputeHash160
core/smart_contract/script_interpreter.go:398:30: unused method ScriptInterpreter.ComputeMerkleRoot
core/smart_contract/transaction_monitor.go:407:6: unused function ContractEventHandler
core/smart_contract/types.go:10:2: unused const ContractStatusCreated
core/smart_contract/types.go:11:2: unused const ContractStatusActive
core/smart_contract/types.go:12:2: unused const ContractStatusFunded
core/smart_contract/types.go:13:2: unused const ContractStatusConfirmed
core/smart_contract/types.go:14:2: unused const ContractStatusExpired
core/smart_contract/types.go:27:2: unused const ProposalStatusRejected
core/smart_contract/types.go:34:2: unused const ClaimStatusExpired
core/smart_contract/types.go:40:2: unused const SubmissionStatusApproved
core/smart_contract/types.go:48:2: unused const StatusPending
core/smart_contract/types.go:49:2: unused const StatusActive
core/smart_contract/types.go:50:2: unused const StatusCompleted
core/smart_contract/types.go:51:2: unused const StatusAll
core/types.go:65:6: unused type BatchItem
core/types.go:130:6: unused type BlockWithCounts
core/types.go:229:6: unused function NewHealthResponse
core/types.go:270:24: unused method ErrorResponse.ToJSON
handlers/auth_handler.go:41:25: unused method APIKeyHandler.HandleRegister
handlers/health_discovery_handler.go:128:24: unused method BlockHandler.HandleGetBlocks
handlers/inscription_handler.go:206:6: unused function wishKeyFromText
handlers/inscription_handler.go:225:6: unused function proposalContractID
handlers/inscription_handler.go:241:6: unused function ingestionContractID
handlers/inscription_handler.go:254:6: unused function isRejectedProposalStatus
handlers/inscription_handler.go:278:6: unused function computeVisiblePixelHash
handlers/inscription_handler.go:353:6: unused function ensureIngestionImageFile
handlers/inscription_handler.go:1072:30: unused method InscriptionHandler.fromProposal
handlers/inscription_handler.go:1128:30: unused method InscriptionHandler.upsertOpenContract
handlers/inscription_handler.go:1209:30: unused method InscriptionHandler.updateContractID
handlers/security_test.go:80:6: unused function hasSeparators
handlers/smart_contract_handler.go:55:6: unused function includeConfirmedQuery
handlers/smart_contract_handler.go:146:6: unused function proofsConfirmed
handlers/smart_contract_handler.go:497:32: unused method SmartContractHandler.HandleCreateContract
handlers/smart_contract_handler.go:519:32: unused method SmartContractHandler.HandleGetContract
mcp/auth.go:35:25: unused method HTTPMCPServer.authWrap
mcp/errors.go:69:2: unused const ErrCodeAlreadyExists
mcp/errors.go:72:2: unused const ErrCodeForbidden
mcp/errors.go:78:2: unused const ErrCodeBadGateway
mcp/errors.go:84:2: unused const ToolPrefixApproveProposal
mcp/errors.go:125:27: unused method ValidationError.ToToolError
mcp/errors.go:171:6: unused function NewMissingFieldError
mcp/errors.go:208:6: unused function NewConflictError
mcp/errors.go:305:6: unused function GetHTTPStatusFromError
mcp/http_mcp_server.go:569:25: unused method HTTPMCPServer.statusFromError
mcp/http_mcp_server.go:2432:25: unused method HTTPMCPServer.verifyBTCSignature
mcp/utils.go:12:6: unused function requireCreatorApproval
middleware/middleware.go:249:6: unused function ContentType
middleware/middleware.go:276:6: unused function RateLimit
middleware/security.go:9:6: unused function ValidateFilename
models/models.go:27:6: unused type SmartContractImage
models/models.go:30:6: unused type ContractMetadata
models/models.go:40:6: unused type Block
models/models.go:82:6: unused type HealthResponse
models/models.go:89:6: unused type SuccessResponse
models/models.go:95:6: unused type QRCodeRequest
models/models.go:101:6: unused type BlockImagesRequest
models/models.go:106:6: unused type BlockImagesResponse
models/models.go:127:6: unused type SearchRequest
models/models.go:139:6: unused type SmartContractsResponse
models/models.go:173:6: unused function NewErrorResponseWithHint
models/models.go:186:6: unused function NewSuccessResponseWithMeta
services/ingestion_service.go:20:2: unused type IngestUpdateRow
services/services.go:64:30: unused method InscriptionService.CreateInscription
starlight/proxy_scanner.go:26:6: unused function NewProxyScanner
starlight/proxy_scanner.go:415:24: unused method ProxyScanner.doRequestWithRetry
starlight/scanner_manager.go:140:27: unused method ScannerManager.ScanBlock
stego/alpha.go:181:6: unused function GetAlphaBits
storage/auth/apikey_store_gorm.go:28:18: unused method APIKeyRow.TableName
storage/auth/apikey_store_gorm.go:93:27: unused method GORMAPIKeyStore.DB
storage/auth/challenge_store.go:36:6: unused function NewAIChallengeStore
storage/auth/challenge_store.go:146:26: unused method ChallengeStore.Get
storage/auth/challenge_store.go:154:26: unused method ChallengeStore.Delete
storage/config.go:154:6: unused function DefaultDataDir
storage/gormdb/db_test.go:37:23: unused method autoMigrateRow.TableName
storage/gormdb/sqlite_dialector.go:197:33: unused method moderncSQLiteDialector.SavePoint
storage/gormdb/sqlite_dialector.go:201:33: unused method moderncSQLiteDialector.RollbackTo
storage/ingestion/service.go:245:6: unused function GetIngestionSchema
storage/ingestion/service.go:304:28: unused method IngestionService.GetByImageAndMessage
storage/ingestion/service.go:469:28: unused method IngestionService.UpdateID
storage/ipfs/mirror.go:72:18: unused method Mirror.OnFileDownloaded
storage/smart_contract/contract_cache.go:77:25: unused method ContractCache.Invalidate
storage/smart_contract/contract_cache.go:85:25: unused method ContractCache.InvalidateByContract
storage/smart_contract/gorm_models.go:23:21: unused method gormClaimRow.TableName
storage/smart_contract/gorm_models.go:60:20: unused method gormTaskRow.TableName
storage/smart_contract/notifying_store.go:68:26: unused method NotifyingStore.Unwrap
storage/smart_contract/pg_store_validation_test.go:121:6: unused function setupTestPGStore
storage/smart_contract/proposal_prepare.go:114:6: unused function WishIDToSupersedeOnApproval
storage/smart_contract/proposals_repository.go:19:6: unused function NewProposalsRepository
storage/smart_contract/proposals_repository.go:24:31: unused method ProposalsRepository.List
storage/smart_contract/proposals_repository.go:89:31: unused method ProposalsRepository.Get
storage/smart_contract/proposals_repository.go:108:31: unused method ProposalsRepository.Create
storage/smart_contract/proposals_repository.go:168:31: unused method ProposalsRepository.UpdateMetadata
storage/smart_contract/rate_limiter.go:72:6: unused type SecurityContext
storage/smart_contract/rate_limiter.go:129:28: unused method SecurityManager.MarkSuspicious
storage/smart_contract/rate_limiter.go:149:28: unused method SecurityManager.GetSecurityStatus
storage/smart_contract/rate_limiter.go:164:6: unused function SecurityMiddleware
storage/smart_contract/rate_limiter.go:169:6: unused function ValidateAPIRequest
storage/smart_contract/rate_limiter.go:247:24: unused method AuditLogger.GetRecentEvents
storage/smart_contract/rate_limiter.go:262:6: unused function LogSecurityEvent
storage/smart_contract/schema_manager.go:15:6: unused function NewSchemaManager
storage/smart_contract/schema_manager.go:20:25: unused method SchemaManager.Initialize
storage/smart_contract/schema_manager.go:35:25: unused method SchemaManager.SeedFixtures
storage/smart_contract/security_utils.go:413:6: unused function IsValidStatus
storage/smart_contract/security_utils.go:455:6: unused function SanitizeFileName
storage/smart_contract/sql_dialect.go:80:20: unused method SQLStore.contractSkillsSelect
storage/smart_contract/sql_dialect.go:106:6: unused function formatTimePtr
storage/smart_contract/sql_store.go:86:20: unused method SQLStore.queryRow
storage/smart_contract/tasks_repository.go:20:6: unused function NewTasksRepository
storage/smart_contract/tasks_repository.go:25:27: unused method TasksRepository.List
storage/smart_contract/tasks_repository.go:102:27: unused method TasksRepository.Upsert
storage/smart_contract/tasks_repository.go:131:27: unused method TasksRepository.UpdateProof
storage/smart_contract/tasks_repository.go:159:27: unused method TasksRepository.Status
storage/smart_contract/utils.go:39:6: unused function budgetFromMeta
storage/smart_contract/utils.go:64:6: unused function IsValidHash
storage/sql_data_storage.go:31:21: unused method BlockScanRow.TableName
storage/sql_data_storage.go:97:2: unused type PostgresStorage
storage/sql_data_storage.go:99:2: unused type SQLiteDataStorage
```

---

## Appendix B — JavaScript findings (71)

```
eslint.config.cjs:1:1: unused file eslint.config.cjs                          # IGNORE (tool entry)
postcss.config.js:1:1: unused file postcss.config.js                          # IGNORE
scripts/generate-sitemap.js:1:1: unused file scripts/generate-sitemap.js      # IGNORE (npm script)
src/App.jsx:1:1: unused import React                                          # drop import (JSX runtime)
src/App.jsx:14:7: unused var InscriptionModal                                 # FALSE (lazy + JSX)
src/App.jsx:27:1: unused import API_BASE                                      # REVIEW (likely real)
src/App.jsx:27:1: unused import CONTENT_BASE                                  # FALSE (template string)
src/App.test.jsx:1:1: unused file src/App.test.jsx                            # IGNORE (vitest)
src/StegoAnalysisViewer.jsx:1:1: unused file                                  # P0 review
src/components/Block/BlockCard.jsx:1:1: unused import React                   # drop import
src/components/Block/BlockCard.jsx:1:1: unused import useState                # REVIEW (likely real)
src/components/Block/BlockCard.jsx:3:7: unused var BlockCard                  # FALSE (export default)
src/components/Block/OpenContractsView.jsx:1:1: unused import React           # drop import
src/components/Block/OpenContractsView.jsx:3:1: unused import API_BASE        # REVIEW (check templates)
src/components/Block/OpenContractsView.jsx:86:7: unused var OpenContractsView # FALSE (export default)
src/components/Block/OpenContractsView.test.jsx:1:1: unused file              # IGNORE
src/components/Common/AppHeader.jsx:1:1: unused import React                  # drop import
src/components/Common/AppHeader.jsx:2:1: unused import Menu                   # REVIEW (likely real)
src/components/Common/AppHeader.jsx:17:7: unused var AppHeader                # FALSE (export default)
src/components/Common/ConfidenceIndicator.jsx:1:1: unused file                # P0 review
src/components/Common/CopyButton.jsx:1:1: unused import React                 # drop import
src/components/Common/CopyButton.jsx:4:7: unused var CopyButton               # FALSE (export default)
src/components/Common/MarkdownContent.jsx:1:1: unused import React            # drop import
src/components/Common/MarkdownContent.test.jsx:1:1: unused file               # IGNORE
src/components/Common/SafeQrCodeCanvas.jsx:26:7: unused var SafeQrCodeCanvas  # FALSE (export default)
src/components/Discover/DiscoverPage.jsx:1:1: unused import React             # drop import
src/components/Discover/DiscoverPage.jsx:4:1: unused import API_BASE          # REVIEW (check templates)
src/components/Inscription/InscriptionCard.jsx:1:1: unused import React       # drop import
src/components/Inscription/InscriptionCard.jsx:3:7: unused var InscriptionCard# FALSE
src/components/Inscription/InscriptionContentTab.jsx:1:1: unused file         # P0 review
src/components/Inscription/InscriptionModal.jsx:1:1: unused import React      # drop import
src/components/Inscription/InscriptionModal.jsx:7:1: unused import DeliverablesReview  # REVIEW
src/components/Inscription/InscriptionModal.jsx:9:1: unused import shouldShowProposalAction # REVIEW
src/components/Inscription/InscriptionModal.jsx:12:7: unused var InscriptionModal # FALSE
src/components/Inscription/InscriptionModal.test.js:1:1: unused file          # IGNORE
src/components/Inscription/WishChatModal.jsx:1:1: unused file                 # FALSE (re-export)
src/components/Inscription/WishChatModal.test.jsx:1:1: unused file            # IGNORE
src/components/Inscription/useInscriptionModalState.js:3:1: unused import API_BASE # REVIEW
src/components/Inscription/wishChatBot.js:1:1: unused file                    # FALSE (via WishChatModal)
src/components/Inscription/wishChatBot.test.js:1:1: unused file               # IGNORE
src/components/Review/DeliverablesReview.jsx:1:1: unused import React         # drop import
src/components/Review/DeliverablesReview.jsx:2:1: unused import Clock         # REVIEW
src/components/Review/DeliverablesReview.jsx:2:1: unused import Code          # REVIEW
src/components/Review/DeliverablesReview.jsx:2:1: unused import FileText      # REVIEW
src/components/Review/DeliverablesReview.jsx:4:1: unused import getSubmissionTimestamp # REVIEW
src/components/Review/DeliverablesReview.jsx:15:7: unused var DeliverablesReview # FALSE
src/components/Review/DeliverablesReview.test.jsx:1:1: unused file            # IGNORE
src/components/Review/SubmissionReview.jsx:1:1: unused file                   # P0 review
src/context/AuthContext.jsx:1:1: unused import React                         # drop import
src/context/AuthContext.jsx:1:1: unused import useContext                    # REVIEW
src/context/AuthContext.jsx:8:7: unused var API_BASE                         # REVIEW
src/context/ThemeContext.jsx:2:1: unused import React                        # drop import
src/context/ThemeContext.jsx:2:1: unused import useContext                   # REVIEW
src/hooks/useBlocks.js:2:1: unused import API_BASE                           # REVIEW (templates)
src/hooks/useContracts.js:2:1: unused import API_BASE                        # REVIEW
src/hooks/useContracts.js:2:1: unused import CONTENT_BASE                    # REVIEW
src/hooks/useInscriptions.js:2:1: unused import API_BASE                     # REVIEW
src/hooks/useInscriptions.js:2:1: unused import CONTENT_BASE                 # REVIEW
src/pages/AuthPage.jsx:1:1: unused import React                              # drop import
src/pages/ContractsPage.jsx:1:1: unused import React                         # drop import
src/pages/DocsPage.jsx:1:1: unused import React                              # drop import
src/pages/McpDocsPage.jsx:1:1: unused import React                           # drop import
src/pages/McpDocsPage.jsx:2:1: unused import API_BASE                        # REVIEW
src/setupTests.js:1:1: unused file src/setupTests.js                         # IGNORE
src/utils/api.js:1:1: unused import API_BASE                                 # REVIEW
src/utils/api.test.js:1:1: unused file                                       # IGNORE
src/utils/db.js:96:17: unused export saveDB                                  # drop export, keep fn
tests/mcp_workflow.spec.js:1:1: unused file                                  # IGNORE (playwright)
tests/mcp_workflow_real.spec.js:1:1: unused file                             # IGNORE
tests/selection.spec.js:1:1: unused file                                     # IGNORE
vite.config.js:1:1: unused file vite.config.js                               # IGNORE
```

CSS raw list is 386 lines; not copied here. Filter `src/App.css` and non-utility `src/index.css` from `/tmp/dcd-stargate/js-css.txt` if a CSS pass is approved.
