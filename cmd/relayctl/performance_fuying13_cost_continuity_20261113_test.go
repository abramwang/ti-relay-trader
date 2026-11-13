package main

import "testing"

func TestFuyingCostContinuityNoWriteMode(t *testing.T) {
	for _, args := range [][]string{nil, {"-apply"}, {"-persist"}} {
		if runFuyingCostContinuity(args) == nil {
			t.Fatal("missing output or write mode accepted")
		}
	}
}
