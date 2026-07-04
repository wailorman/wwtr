package trust_test

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/wailorman/wwtr/internal/di/fakes"
	"github.com/wailorman/wwtr/internal/trust"
)

const (
	configA      = "/repo/.wwtr.yml"
	registryPath = "/cfg/wwtr/trust.yaml"
)

// writeFailingFS wraps a FakeFS but returns a scripted error on WriteFile for
// the configured path. FakeFS.InjectError is consumed by the very next op on a
// path (including reads), so it cannot isolate a write-only failure on the
// registry path — this wrapper can.
type writeFailingFS struct {
	*fakes.FakeFS
	failPath string
	err      error
}

func (w *writeFailingFS) WriteFile(path string, data []byte, perm os.FileMode) error {
	if path == w.failPath {
		return w.err
	}
	return w.FakeFS.WriteFile(path, data, perm)
}

// newStore seeds a FakeFS with a config at configA and returns the Store, the
// FS (for injecting errors or rewriting content) and the prompter.
func newStore(t *testing.T, content string) (trust.Store, *fakes.FakeFS) {
	t.Helper()
	fsys := fakes.NewFakeFS()
	if err := fsys.WriteFile(configA, []byte(content), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	return trust.NewStore(fsys, registryPath), fsys
}

func TestCheck_Trusted(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, "version: 1\n")
	if err := store.Add(configA); err != nil {
		t.Fatalf("Add: %v", err)
	}
	st, err := store.Check(configA)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if st != trust.StatusTrusted {
		t.Fatalf("status=%v want Trusted", st)
	}
}

func TestCheck_Changed(t *testing.T) {
	t.Parallel()
	store, fsys := newStore(t, "version: 1\n")
	if err := store.Add(configA); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := fsys.WriteFile(configA, []byte("version: 2 # changed\n"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	st, err := store.Check(configA)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if st != trust.StatusChanged {
		t.Fatalf("status=%v want Changed", st)
	}
}

func TestCheck_Unknown(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, "version: 1\n")
	st, err := store.Check(configA)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if st != trust.StatusUnknown {
		t.Fatalf("status=%v want Unknown", st)
	}
}

func TestCheck_RegistryMissing_TreatedAsUnknown(t *testing.T) {
	t.Parallel()
	// Fresh FS, registry file does not exist at all.
	fsys := fakes.NewFakeFS()
	if err := fsys.WriteFile(configA, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := trust.NewStore(fsys, registryPath)
	st, err := store.Check(configA)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if st != trust.StatusUnknown {
		t.Fatalf("status=%v want Unknown when registry missing", st)
	}
}

func TestCheck_ConfigReadError(t *testing.T) {
	t.Parallel()
	store, fsys := newStore(t, "version: 1\n")
	ioErr := errors.New("disk on fire")
	fsys.InjectError(configA, ioErr)
	_, err := store.Check(configA)
	if err == nil || !strings.Contains(err.Error(), "read config") {
		t.Fatalf("err=%v want wrap of read config", err)
	}
	if !errors.Is(err, ioErr) {
		t.Fatalf("err=%v should wrap ioErr", err)
	}
}

func TestCheck_RegistryCorrupt(t *testing.T) {
	t.Parallel()
	store, fsys := newStore(t, "version: 1\n")
	if err := fsys.WriteFile(registryPath, []byte("not: [valid: yaml: {{{"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := store.Check(configA)
	if err == nil || !strings.Contains(err.Error(), "read registry") {
		t.Fatalf("err=%v want registry parse failure", err)
	}
}

func TestCheck_RegistryReadError(t *testing.T) {
	t.Parallel()
	store, fsys := newStore(t, "version: 1\n")
	// A non-ErrNotExist read failure surfaces verbatim from loadRegistry.
	regErr := errors.New("permission denied")
	fsys.InjectError(registryPath, regErr)
	_, err := store.Check(configA)
	if err == nil || !errors.Is(err, regErr) {
		t.Fatalf("err=%v want wrap of regErr", err)
	}
}

func TestAdd_RegistryCorrupt(t *testing.T) {
	t.Parallel()
	store, fsys := newStore(t, "version: 1\n")
	if err := fsys.WriteFile(registryPath, []byte("not: [valid: yaml: {{{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(configA); err == nil ||
		!strings.Contains(err.Error(), "read registry") {
		t.Fatalf("err=%v want read registry failure", err)
	}
}

func TestAdd_NewEntry(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, "version: 1\n")
	if err := store.Add(configA); err != nil {
		t.Fatalf("Add: %v", err)
	}
	all, err := store.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	hash, ok := all[configA]
	if !ok {
		t.Fatalf("entry missing after Add: %v", all)
	}
	if len(hash) != 64 {
		t.Fatalf("hash len=%d want 64 (sha-256 hex)", len(hash))
	}
	// Stable against content: recompute and compare.
	st, err := store.Check(configA)
	if err != nil || st != trust.StatusTrusted {
		t.Fatalf("after Add, Check=%v err=%v want Trusted", st, err)
	}
}

func TestAdd_OverwriteExisting(t *testing.T) {
	t.Parallel()
	store, fsys := newStore(t, "version: 1\n")
	if err := store.Add(configA); err != nil {
		t.Fatal(err)
	}
	before, _ := store.All()
	if err := fsys.WriteFile(configA, []byte("version: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(configA); err != nil {
		t.Fatal(err)
	}
	after, _ := store.All()
	if before[configA] == after[configA] {
		t.Fatalf("hash did not change after overwrite: %s", after[configA])
	}
}

func TestAdd_CreatesParentDir(t *testing.T) {
	t.Parallel()
	// Registry lives in a directory that does not exist yet in the FakeFS.
	fsys := fakes.NewFakeFS()
	nested := "/deep/nested/dir/trust.yaml"
	if err := fsys.WriteFile(configA, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := trust.NewStore(fsys, nested)
	if err := store.Add(configA); err != nil {
		t.Fatalf("Add with nested registry: %v", err)
	}
	if !fsys.Exists(nested) {
		t.Fatalf("registry file not created at %s", nested)
	}
}

func TestAdd_ConfigReadError(t *testing.T) {
	t.Parallel()
	store, fsys := newStore(t, "version: 1\n")
	fsys.InjectError(configA, errors.New("boom"))
	if err := store.Add(configA); err == nil {
		t.Fatal("Add should fail on config read error")
	}
}

func TestAdd_WriteError(t *testing.T) {
	t.Parallel()
	fsys := fakes.NewFakeFS()
	if err := fsys.WriteFile(configA, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeErr := errors.New("cannot write")
	store := trust.NewStore(&writeFailingFS{FakeFS: fsys, failPath: registryPath, err: writeErr}, registryPath)
	if err := store.Add(configA); err == nil ||
		!strings.Contains(err.Error(), "write registry") {
		t.Fatalf("err=%v want write registry failure", err)
	}
}

func TestAdd_MkdirAllError(t *testing.T) {
	t.Parallel()
	store, fsys := newStore(t, "version: 1\n")
	fsys.InjectError("/cfg/wwtr", errors.New("nope"))
	if err := store.Add(configA); err == nil ||
		!strings.Contains(err.Error(), "create registry dir") {
		t.Fatalf("err=%v want create registry dir failure", err)
	}
}

func TestRemove_Existing(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, "version: 1\n")
	if err := store.Add(configA); err != nil {
		t.Fatal(err)
	}
	if err := store.Remove(configA); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	all, _ := store.All()
	if _, ok := all[configA]; ok {
		t.Fatalf("entry still present after Remove: %v", all)
	}
}

func TestRemove_Missing_NoOp(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, "version: 1\n")
	// Populate registry with a different config so the file exists.
	if err := store.Add(configA); err != nil {
		t.Fatal(err)
	}
	missing := "/elsewhere/.wwtr.yml"
	if err := store.Remove(missing); err != nil {
		t.Fatalf("Remove absent entry: %v", err)
	}
}

func TestRemove_RegistryMissing_NoOp(t *testing.T) {
	t.Parallel()
	fsys := fakes.NewFakeFS()
	store := trust.NewStore(fsys, registryPath)
	if err := store.Remove(configA); err != nil {
		t.Fatalf("Remove on missing registry should be no-op: %v", err)
	}
	if fsys.Exists(registryPath) {
		t.Fatalf("registry should not have been created by no-op Remove")
	}
}

func TestRemove_WriteError(t *testing.T) {
	t.Parallel()
	fsys := fakes.NewFakeFS()
	if err := fsys.WriteFile(configA, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Seed the entry with a non-failing store first.
	seedStore := trust.NewStore(fsys, registryPath)
	if err := seedStore.Add(configA); err != nil {
		t.Fatalf("seed Add: %v", err)
	}
	// Now swap in a failing WriteFile for the Remove.
	writeErr := errors.New("write failed")
	failingStore := trust.NewStore(
		&writeFailingFS{FakeFS: fsys, failPath: registryPath, err: writeErr},
		registryPath,
	)
	if err := failingStore.Remove(configA); err == nil ||
		!strings.Contains(err.Error(), "write registry") {
		t.Fatalf("err=%v want write registry failure", err)
	}
}

func TestRemove_RegistryCorrupt(t *testing.T) {
	t.Parallel()
	fsys := fakes.NewFakeFS()
	if err := fsys.WriteFile(configA, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fsys.WriteFile(registryPath, []byte(": : : not yaml {{{"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := trust.NewStore(fsys, registryPath)
	if err := store.Remove(configA); err == nil ||
		!strings.Contains(err.Error(), "read registry") {
		t.Fatalf("err=%v want read registry failure", err)
	}
}

func TestAll_Empty_NoFile(t *testing.T) {
	t.Parallel()
	fsys := fakes.NewFakeFS()
	store := trust.NewStore(fsys, registryPath)
	all, err := store.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if all == nil || len(all) != 0 {
		t.Fatalf("want non-nil empty map, got %v", all)
	}
}

func TestAll_Populated(t *testing.T) {
	t.Parallel()
	store, fsys := newStore(t, "version: 1\n")
	if err := store.Add(configA); err != nil {
		t.Fatal(err)
	}
	// Seed a second config and add it too.
	other := "/other/.wwtr.yml"
	if err := fsys.WriteFile(other, []byte("version: 9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(other); err != nil {
		t.Fatal(err)
	}
	all, err := store.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("len(all)=%d want 2", len(all))
	}
	for _, h := range all {
		if len(h) != 64 {
			t.Fatalf("hash len=%d want 64", len(h))
		}
	}
}

// --- Ensure ---------------------------------------------------------------

func TestEnsure_Trusted_ReturnsNil(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, "version: 1\n")
	if err := store.Add(configA); err != nil {
		t.Fatal(err)
	}
	prompter := &fakes.FakePrompter{}
	tty := fakes.FakeTTY{Interactive: true}
	if err := trust.Ensure(store, prompter, tty, configA, false); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if len(prompter.Calls) != 0 {
		t.Fatalf("trusted config must not prompt; calls=%v", prompter.Calls)
	}
}

func TestEnsure_Unknown_Interactive_Yes(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, "version: 1\n")
	prompter := &fakes.FakePrompter{Confirms: []bool{true}}
	tty := fakes.FakeTTY{Interactive: true}
	if err := trust.Ensure(store, prompter, tty, configA, false); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if len(prompter.Calls) != 1 {
		t.Fatalf("want exactly 1 prompt, got %d", len(prompter.Calls))
	}
	// Add should have recorded the config — Verify via Check.
	st, _ := store.Check(configA)
	if st != trust.StatusTrusted {
		t.Fatalf("after yes, status=%v want Trusted", st)
	}
}

func TestEnsure_Unknown_Interactive_No(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, "version: 1\n")
	prompter := &fakes.FakePrompter{Confirms: []bool{false}}
	tty := fakes.FakeTTY{Interactive: true}
	err := trust.Ensure(store, prompter, tty, configA, false)
	if !errors.Is(err, trust.ErrDenied) {
		t.Fatalf("err=%v want ErrDenied", err)
	}
	if fsysExists(store) {
		t.Fatalf("registry should not be written on denial")
	}
}

// fsysExists checks whether the registry has any entry for configA via All.
func fsysExists(store trust.Store) bool {
	all, err := store.All()
	if err != nil {
		return false
	}
	_, ok := all[configA]
	return ok
}

func TestEnsure_Changed_Interactive_No(t *testing.T) {
	t.Parallel()
	store, fsys := newStore(t, "version: 1\n")
	if err := store.Add(configA); err != nil {
		t.Fatal(err)
	}
	if err := fsys.WriteFile(configA, []byte("version: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prompter := &fakes.FakePrompter{Confirms: []bool{false}}
	tty := fakes.FakeTTY{Interactive: true}
	err := trust.Ensure(store, prompter, tty, configA, false)
	if !errors.Is(err, trust.ErrDenied) {
		t.Fatalf("err=%v want ErrDenied", err)
	}
}

func TestEnsure_Changed_Interactive_Yes(t *testing.T) {
	t.Parallel()
	store, fsys := newStore(t, "version: 1\n")
	if err := store.Add(configA); err != nil {
		t.Fatal(err)
	}
	if err := fsys.WriteFile(configA, []byte("version: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prompter := &fakes.FakePrompter{Confirms: []bool{true}}
	tty := fakes.FakeTTY{Interactive: true}
	if err := trust.Ensure(store, prompter, tty, configA, false); err != nil {
		t.Fatalf("Ensure after change with yes: %v", err)
	}
	st, _ := store.Check(configA)
	if st != trust.StatusTrusted {
		t.Fatalf("after re-approve, status=%v want Trusted", st)
	}
}

func TestEnsure_NonInteractive_NoYes(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, "version: 1\n")
	prompter := &fakes.FakePrompter{}
	tty := fakes.FakeTTY{Interactive: false}
	err := trust.Ensure(store, prompter, tty, configA, false)
	if !errors.Is(err, trust.ErrNeedsApproval) {
		t.Fatalf("err=%v want ErrNeedsApproval", err)
	}
	if len(prompter.Calls) != 0 {
		t.Fatalf("non-interactive must not prompt; calls=%v", prompter.Calls)
	}
}

func TestEnsure_AutoYes_ApprovesWithoutPrompt_Interactive(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, "version: 1\n")
	prompter := &fakes.FakePrompter{} // no scripted answers — must not be called
	tty := fakes.FakeTTY{Interactive: true}
	if err := trust.Ensure(store, prompter, tty, configA, true); err != nil {
		t.Fatalf("Ensure autoYes: %v", err)
	}
	if len(prompter.Calls) != 0 {
		t.Fatalf("autoYes must not prompt; calls=%v", prompter.Calls)
	}
	st, _ := store.Check(configA)
	if st != trust.StatusTrusted {
		t.Fatalf("status=%v want Trusted", st)
	}
}

func TestEnsure_AutoYes_NonInteractive(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, "version: 1\n")
	prompter := &fakes.FakePrompter{}
	tty := fakes.FakeTTY{Interactive: false}
	if err := trust.Ensure(store, prompter, tty, configA, true); err != nil {
		t.Fatalf("Ensure autoYes non-tty: %v", err)
	}
	st, _ := store.Check(configA)
	if st != trust.StatusTrusted {
		t.Fatalf("status=%v want Trusted", st)
	}
}

func TestEnsure_PrompterError_Propagates(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, "version: 1\n")
	pErr := errors.New("huh blew up")
	// FakePrompter pops Confirms first; supply one so the scripted ConfirmErr
	// is actually returned.
	prompter := &fakes.FakePrompter{Confirms: []bool{true}, ConfirmErr: []error{pErr}}
	tty := fakes.FakeTTY{Interactive: true}
	err := trust.Ensure(store, prompter, tty, configA, false)
	if err == nil || !errors.Is(err, pErr) {
		t.Fatalf("err=%v want wrap of pErr", err)
	}
	if !strings.Contains(err.Error(), "trust prompt") {
		t.Fatalf("err=%v should mention trust prompt", err)
	}
}

func TestEnsure_CheckError_Propagates(t *testing.T) {
	t.Parallel()
	store, fsys := newStore(t, "version: 1\n")
	fsys.InjectError(configA, errors.New("read failed"))
	prompter := &fakes.FakePrompter{}
	tty := fakes.FakeTTY{Interactive: true}
	err := trust.Ensure(store, prompter, tty, configA, false)
	if err == nil || !strings.Contains(err.Error(), "trust:") {
		t.Fatalf("err=%v want Check failure wrapped", err)
	}
}

func TestEnsure_AddError_Propagates(t *testing.T) {
	t.Parallel()
	fsys := fakes.NewFakeFS()
	if err := fsys.WriteFile(configA, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeErr := errors.New("write failed")
	store := trust.NewStore(&writeFailingFS{FakeFS: fsys, failPath: registryPath, err: writeErr}, registryPath)
	prompter := &fakes.FakePrompter{Confirms: []bool{true}}
	tty := fakes.FakeTTY{Interactive: true}
	err := trust.Ensure(store, prompter, tty, configA, false)
	if err == nil || !strings.Contains(err.Error(), "write registry") {
		t.Fatalf("err=%v want write registry failure", err)
	}
}

func TestEnsure_AutoYes_AddError_Propagates(t *testing.T) {
	t.Parallel()
	fsys := fakes.NewFakeFS()
	if err := fsys.WriteFile(configA, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeErr := errors.New("write failed")
	store := trust.NewStore(&writeFailingFS{FakeFS: fsys, failPath: registryPath, err: writeErr}, registryPath)
	prompter := &fakes.FakePrompter{}
	tty := fakes.FakeTTY{Interactive: true}
	err := trust.Ensure(store, prompter, tty, configA, true)
	if err == nil || !strings.Contains(err.Error(), "write registry") {
		t.Fatalf("err=%v want write registry failure", err)
	}
}

// --- Edge cases -----------------------------------------------------------

func TestRoundTrip_UnicodePath(t *testing.T) {
	t.Parallel()
	fsys := fakes.NewFakeFS()
	cfg := "/repo/プロジェクト/設定/.wwtr.yml"
	if err := fsys.WriteFile(cfg, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := trust.NewStore(fsys, registryPath)
	if err := store.Add(cfg); err != nil {
		t.Fatalf("Add unicode path: %v", err)
	}
	st, err := store.Check(cfg)
	if err != nil || st != trust.StatusTrusted {
		t.Fatalf("Check unicode: st=%v err=%v", st, err)
	}
	all, _ := store.All()
	if _, ok := all[cfg]; !ok {
		t.Fatalf("unicode path missing from All(): %v", all)
	}
}

func TestRoundTrip_LargeConfig(t *testing.T) {
	t.Parallel()
	fsys := fakes.NewFakeFS()
	// 1 MiB of config — exercises hashing on a non-trivial payload while
	// keeping the test fast (in-memory FS).
	body := make([]byte, 1<<20)
	for i := range body {
		body[i] = byte(i % 256)
	}
	if err := fsys.WriteFile(configA, body, 0o644); err != nil {
		t.Fatal(err)
	}
	store := trust.NewStore(fsys, registryPath)
	if err := store.Add(configA); err != nil {
		t.Fatalf("Add large: %v", err)
	}
	st, err := store.Check(configA)
	if err != nil || st != trust.StatusTrusted {
		t.Fatalf("Check large: st=%v err=%v", st, err)
	}
	// Mutate one byte → must flip status to Changed.
	body[0] ^= 0xff
	if err := fsys.WriteFile(configA, body, 0o644); err != nil {
		t.Fatal(err)
	}
	st, _ = store.Check(configA)
	if st != trust.StatusChanged {
		t.Fatalf("status after mutation=%v want Changed", st)
	}
}

// Ensure the sentinel errors are distinguishable and exported.
func TestSentinelErrors(t *testing.T) {
	t.Parallel()
	if errors.Is(trust.ErrDenied, trust.ErrNeedsApproval) {
		t.Fatal("ErrDenied and ErrNeedsApproval must differ")
	}
	if !errors.Is(trust.ErrDenied, trust.ErrDenied) {
		t.Fatal("ErrDenied must satisfy errors.Is with itself")
	}
}

// Sanity: io/fs.ErrNotExist is what FakeFS returns — prove the not-exist path
// in loadRegistry is reachable through a real os error too (belt + braces).
func TestNotExistSentinelCompatibility(t *testing.T) {
	t.Parallel()
	if !errors.Is(os.ErrNotExist, fs.ErrNotExist) {
		t.Fatal("os.ErrNotExist must satisfy fs.ErrNotExist")
	}
}
