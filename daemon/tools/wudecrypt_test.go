package tools_test

import (
	"context"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

func TestWuDecrypt_Decrypt_Success(t *testing.T) {
	fakeBin(t, "wudecrypt", `echo "wudecrypt: extracting GM partition"
exit 0
`)
	sink := &ps3RecordingSink{}
	w := tools.NewWuDecrypt("wudecrypt")
	err := w.Decrypt(context.Background(), "/spool/rip.iso", "/spool/decrypted", "/keys/common.key", "/keys/abc.key", sink)
	if err != nil {
		t.Fatal(err)
	}
	if len(sink.logs) == 0 {
		t.Error("want stdout forwarded to sink.Log")
	}
}

func TestWuDecrypt_Decrypt_Failure(t *testing.T) {
	fakeBin(t, "wudecrypt", `echo "wudecrypt: bad common key"
exit 1
`)
	w := tools.NewWuDecrypt("wudecrypt")
	err := w.Decrypt(context.Background(), "/spool/rip.iso", "/spool/decrypted", "/keys/common.key", "/keys/abc.key", nil)
	if err == nil {
		t.Error("want error on nonzero exit")
	}
}
