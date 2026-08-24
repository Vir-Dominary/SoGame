// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 SoGame Contributors
//
// This file is part of SoGame.
//
// SoGame is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// SoGame is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with SoGame. If not, see <https://www.gnu.org/licenses/>.

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
