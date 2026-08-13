package nbdaemon

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type fakeRemovalRunner struct {
	called      bool
	productCode string
}

func (f *fakeRemovalRunner) Remove(_ context.Context, productCode, _ string) error {
	f.called = true
	f.productCode = productCode
	return nil
}

func TestDaemonRemovalRequiresConfirmation(t *testing.T) {
	runner := &fakeRemovalRunner{}
	err := NewDaemonRemover(runner).Remove(context.Background(), false, "{D656CD63-C692-4494-ABAB-31A779E04E08}", `C:\logs\remove.log`)
	if !errors.Is(err, ErrRemovalNotConfirmed) || runner.called {
		t.Fatalf("error=%v runner called=%v", err, runner.called)
	}
}

func TestDaemonRemovalUsesFixedProductCode(t *testing.T) {
	const productCode = "{D656CD63-C692-4494-ABAB-31A779E04E08}"
	runner := &fakeRemovalRunner{}
	if err := NewDaemonRemover(runner).Remove(context.Background(), true, productCode, `C:\logs\remove.log`); err != nil {
		t.Fatal(err)
	}
	if !runner.called || runner.productCode != productCode {
		t.Fatalf("unexpected runner state: %+v", runner)
	}
	want := []string{"/x", productCode, "/quiet", "/qn", "/norestart", "/l*v", `C:\logs\remove.log`}
	got, err := BuildMSIRemovalArguments(productCode, `C:\logs\remove.log`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("remove args=%q", got)
	}
}

func TestDaemonRemovalRejectsArbitraryProductCode(t *testing.T) {
	if _, err := BuildMSIRemovalArguments(`C:\Windows\System32\cmd.exe`, `C:\logs\remove.log`); err == nil {
		t.Fatal("expected invalid product code to be rejected")
	}
}
