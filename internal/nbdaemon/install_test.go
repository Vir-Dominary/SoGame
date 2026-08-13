package nbdaemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	releasebuild "sogame/internal/releasebuild"
)

type fakeArtifactCheck struct {
	err    error
	called bool
}

func (f *fakeArtifactCheck) Verify(context.Context, string, releasebuild.WindowsArtifact) error {
	f.called = true
	return f.err
}

type fakeMSIRunner struct {
	called  bool
	action  MSIAction
	runHook func(MSIAction, string) error
}

func (f *fakeMSIRunner) Run(_ context.Context, action MSIAction, artifactPath, _ string) error {
	f.called = true
	f.action = action
	if f.runHook != nil {
		return f.runHook(action, artifactPath)
	}
	return nil
}

func TestPrivilegedInstallerVerifiesBeforeExecution(t *testing.T) {
	artifact := filepath.Join(t.TempDir(), "netbird.msi")
	if err := os.WriteFile(artifact, []byte("fake msi payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "install.log")
	check := &fakeArtifactCheck{}
	var stagedPath string
	runner := &fakeMSIRunner{}
	installer := NewPrivilegedInstaller(check, runner)

	runner.runHook = func(action MSIAction, artifact string) error {
		stagedPath = artifact
		return nil
	}
	if err := installer.Execute(context.Background(), MSIInstall, artifact, logPath, releasebuild.WindowsArtifact{}); err != nil {
		t.Fatal(err)
	}
	if !check.called || !runner.called || runner.action != MSIInstall {
		t.Fatalf("unexpected call order result: check=%v runner=%+v", check.called, runner)
	}
	// 执行必须使用专用 staging 副本,而不是原路径,以收窄 TOCTOU 窗口。
	if stagedPath == artifact || stagedPath == "" {
		t.Fatalf("expected a staged copy of the artifact, got %q", stagedPath)
	}
}

func TestPrivilegedInstallerStopsOnVerificationFailure(t *testing.T) {
	check := &fakeArtifactCheck{err: ErrArtifactDigest}
	runner := &fakeMSIRunner{}
	err := NewPrivilegedInstaller(check, runner).Execute(
		context.Background(), MSIRepair, `C:\release\netbird.msi`, `C:\release\repair.log`, releasebuild.WindowsArtifact{},
	)
	if !errors.Is(err, ErrArtifactDigest) || runner.called {
		t.Fatalf("error=%v runner called=%v", err, runner.called)
	}
}

func TestBuildMSIArguments(t *testing.T) {
	install, err := BuildMSIArguments(MSIInstall, `C:\release\netbird.msi`, `C:\logs\install.log`)
	if err != nil {
		t.Fatal(err)
	}
	wantInstall := []string{"/i", `C:\release\netbird.msi`, "/quiet", "/qn", "/norestart", "/l*v", `C:\logs\install.log`, "AUTOSTART=0"}
	if !reflect.DeepEqual(install, wantInstall) {
		t.Fatalf("install args=%q", install)
	}
	repair, err := BuildMSIArguments(MSIRepair, `C:\release\netbird.msi`, `C:\logs\repair.log`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(repair[len(repair)-2:], []string{"REINSTALL=ALL", "REINSTALLMODE=vomus"}) {
		t.Fatalf("repair args=%q", repair)
	}
	if _, err := BuildMSIArguments("arbitrary", "x", "y"); !errors.Is(err, ErrUnsupportedAction) {
		t.Fatalf("unexpected action error=%v", err)
	}
}
