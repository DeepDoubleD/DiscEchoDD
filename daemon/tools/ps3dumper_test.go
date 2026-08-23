package tools_test

import (
	"context"
	"os"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

// fakeBin writes an executable shell script named name into a fresh
// PATH-only temp dir and points PATH at it. Mirrors dvdprober_test.go's
// helper.
func fakeBin(t *testing.T, name, script string) {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/" + name
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

// ps3RecordingSink captures Progress/Log calls for assertions.
type ps3RecordingSink struct {
	progress []float64
	logs     []string
}

func (s *ps3RecordingSink) Progress(pct float64, _ string, _ int) { s.progress = append(s.progress, pct) }
func (s *ps3RecordingSink) Log(_ state.LogLevel, format string, args ...any) {
	s.logs = append(s.logs, format)
}
func (s *ps3RecordingSink) SubStep(string) {}

func TestPS3Dumper_Detect_Success(t *testing.T) {
	fakeBin(t, "ps3dumper-cli", `cat <<'EOF'
PS3DUMPER_RESULT: {"success":true,"product_code":"BLUS30109","title":"Kingdom Hearts","total_bytes":8372944896,"total_files":1234}
EOF
`)
	d := tools.NewPS3Dumper("ps3dumper-cli")
	result, err := d.Detect(context.Background(), "/mnt/ps3disc", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.ProductCode != "BLUS30109" {
		t.Errorf("ProductCode = %q, want BLUS30109", result.ProductCode)
	}
	if result.Title != "Kingdom Hearts" {
		t.Errorf("Title = %q, want Kingdom Hearts", result.Title)
	}
	if result.TotalBytes != 8372944896 {
		t.Errorf("TotalBytes = %d, want 8372944896", result.TotalBytes)
	}
}

func TestPS3Dumper_Detect_Failure(t *testing.T) {
	fakeBin(t, "ps3dumper-cli", `cat <<'EOF'
PS3DUMPER_RESULT: {"success":false,"error":"No valid PS3 disc was detected. Disc must be detected and mounted."}
EOF
exit 1
`)
	d := tools.NewPS3Dumper("ps3dumper-cli")
	_, err := d.Detect(context.Background(), "/mnt/ps3disc", nil)
	if err == nil {
		t.Error("want error when PS3DUMPER_RESULT reports success:false")
	}
}

func TestPS3Dumper_Detect_NonResultOutputIsLoggedNotParsed(t *testing.T) {
	fakeBin(t, "ps3dumper-cli", `cat <<'EOF'
some noisy Log.Info line from IrdLibraryClient
PS3DUMPER_RESULT: {"success":true,"product_code":"BLUS30109","title":"Kingdom Hearts"}
EOF
`)
	sink := &ps3RecordingSink{}
	d := tools.NewPS3Dumper("ps3dumper-cli")
	result, err := d.Detect(context.Background(), "/mnt/ps3disc", sink)
	if err != nil {
		t.Fatal(err)
	}
	if result.ProductCode != "BLUS30109" {
		t.Errorf("ProductCode = %q, want BLUS30109", result.ProductCode)
	}
	if len(sink.logs) == 0 {
		t.Error("want the noisy non-result line to be logged")
	}
}

func TestPS3Dumper_Dump_ProgressAndResult(t *testing.T) {
	fakeBin(t, "ps3dumper-cli", `cat <<'EOF'
PS3DUMPER_PROGRESS: {"processed_sectors":500,"total_sectors":1000,"current_file":5,"total_files":10}
PS3DUMPER_PROGRESS: {"processed_sectors":1000,"total_sectors":1000,"current_file":10,"total_files":10}
PS3DUMPER_RESULT: {"success":true,"product_code":"BLUS30109","title":"Kingdom Hearts","total_bytes":100,"total_files":10,"broken_files":0,"output_subdir":"BLUS30109"}
EOF
`)
	sink := &ps3RecordingSink{}
	d := tools.NewPS3Dumper("ps3dumper-cli")
	result, err := d.Dump(context.Background(), "/mnt/ps3disc", "/spool/out", "/keys", sink)
	if err != nil {
		t.Fatal(err)
	}
	if result.OutputSubdir != "BLUS30109" {
		t.Errorf("OutputSubdir = %q, want BLUS30109", result.OutputSubdir)
	}
	if len(sink.progress) != 2 {
		t.Fatalf("progress calls = %d, want 2", len(sink.progress))
	}
	if sink.progress[0] != 50 {
		t.Errorf("first progress = %v, want 50", sink.progress[0])
	}
	if sink.progress[1] != 100 {
		t.Errorf("second progress = %v, want 100", sink.progress[1])
	}
}

func TestPS3Dumper_Dump_BrokenFilesIsFailure(t *testing.T) {
	fakeBin(t, "ps3dumper-cli", `cat <<'EOF'
PS3DUMPER_RESULT: {"success":false,"broken_files":3}
EOF
exit 1
`)
	d := tools.NewPS3Dumper("ps3dumper-cli")
	result, err := d.Dump(context.Background(), "/mnt/ps3disc", "/spool/out", "/keys", nil)
	if err == nil {
		t.Error("want error when broken_files > 0")
	}
	if result == nil || result.BrokenFiles != 3 {
		t.Errorf("result = %+v, want BrokenFiles=3", result)
	}
}
