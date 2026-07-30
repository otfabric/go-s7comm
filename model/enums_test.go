// SPDX-License-Identifier: MIT

package model

import "testing"

func TestAreaString(t *testing.T) {
	tests := map[Area]string{
		AreaInputs:  "I",
		AreaOutputs: "Q",
		AreaMerkers: "M",
		AreaDB:      "DB",
		AreaCounter: "C",
		AreaTimer:   "T",
		Area(0xFF):  "?",
	}
	for area, want := range tests {
		if got := area.String(); got != want {
			t.Errorf("Area(%#x).String() = %q, want %q", area, got, want)
		}
	}
}

func TestBlockTypeString(t *testing.T) {
	tests := map[BlockType]string{
		BlockOB: "OB", BlockDB: "DB", BlockSDB: "SDB", BlockFC: "FC",
		BlockSFC: "SFC", BlockFB: "FB", BlockSFB: "SFB",
		BlockType(0x00): "?",
	}
	for bt, want := range tests {
		if got := bt.String(); got != want {
			t.Errorf("BlockType(%#x).String() = %q, want %q", bt, got, want)
		}
	}
}

func TestBlockLangString(t *testing.T) {
	tests := map[BlockLang]string{
		BlockLangAWL: "AWL", BlockLangKOP: "KOP", BlockLangFUP: "FUP",
		BlockLangSCL: "SCL", BlockLangDB: "DB", BlockLangGraph: "GRAPH",
		BlockLang(0xFF): "?",
	}
	for bl, want := range tests {
		if got := bl.String(); got != want {
			t.Errorf("BlockLang(%#x).String() = %q, want %q", bl, got, want)
		}
	}
}

func TestCPUStateString(t *testing.T) {
	if CPUStateRun.String() != "RUN" {
		t.Fatalf("unexpected CPU state string: %q", CPUStateRun.String())
	}
	if CPUState(255).String() != "UNKNOWN" {
		t.Fatalf("unexpected unknown CPU state string: %q", CPUState(255).String())
	}
}

func TestProtectionLevelString(t *testing.T) {
	if ProtectionReadWrite.String() != "Read/Write Protected" {
		t.Fatalf("unexpected protection string: %q", ProtectionReadWrite.String())
	}
	if ProtectionLevel(255).String() != "Unknown" {
		t.Fatalf("unexpected unknown protection string: %q", ProtectionLevel(255).String())
	}
}
