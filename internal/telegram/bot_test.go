package telegram

import (
	"testing"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/domain"
)

func TestRegisteredChatModes(t *testing.T) {
	auto := &config.Config{Chats: config.ChatsPolicy{Mode: "auto"}}
	if !RegisteredChat(auto, -100123) {
		t.Error("auto mode should accept any chat")
	}
	allow := &config.Config{Chats: config.ChatsPolicy{Mode: "allowlist", Allowlist: []int64{-100123}}}
	if !RegisteredChat(allow, -100123) {
		t.Error("allowlisted chat should be accepted")
	}
	if RegisteredChat(allow, -100999) {
		t.Error("non-allowlisted chat should be rejected")
	}
}

func TestImmuneKinds(t *testing.T) {
	if !ImmuneSender(domain.Sender{Kind: domain.SenderAnonAdmin}) {
		t.Error("anon admin is immune")
	}
	if !ImmuneSender(domain.Sender{Kind: domain.SenderLinkedChannel}) {
		t.Error("linked channel is immune")
	}
	if ImmuneSender(domain.Sender{Kind: domain.SenderUser}) {
		t.Error("plain user is not immune")
	}
}

func TestUnionMediaKindsMergesAlbumPartsWithoutRepeats(t *testing.T) {
	parts := []domain.Message{
		{MediaKinds: []string{"photo"}},
		{MediaKinds: []string{"photo"}},
		{MediaKinds: []string{"video"}},
	}
	got := unionMediaKinds(parts)
	if len(got) != 2 || got[0] != "photo" || got[1] != "video" {
		t.Fatalf("album kinds = %v, want [photo video]", got)
	}
}

func TestUnionMediaKindsOfATextOnlyAlbumIsEmpty(t *testing.T) {
	if got := unionMediaKinds([]domain.Message{{}, {}}); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestUnionMessageMetadataKeepsDocumentTypeFromAnotherAlbumPart(t *testing.T) {
	parts := []domain.Message{
		{Text: "caption", MediaKinds: []string{"photo"}},
		{MediaKinds: []string{"document"}, DocumentExtensions: []string{".apk"}, DocumentMIMETypes: []string{"application/vnd.android.package-archive"}},
	}
	exts := unionMessageMetadata(parts, func(m domain.Message) []string { return m.DocumentExtensions })
	mimes := unionMessageMetadata(parts, func(m domain.Message) []string { return m.DocumentMIMETypes })
	if len(exts) != 1 || exts[0] != ".apk" || len(mimes) != 1 || mimes[0] != "application/vnd.android.package-archive" {
		t.Fatalf("extensions=%v MIME types=%v", exts, mimes)
	}
}
