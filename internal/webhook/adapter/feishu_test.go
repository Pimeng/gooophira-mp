package adapter

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/config"
)

func TestAnimatedWebPDetection(t *testing.T) {
	animated := append([]byte("RIFF\x10\x00\x00\x00WEBP"), []byte("ANIM\x00\x00\x00\x00")...)
	if !isAnimatedWebP(animated) {
		t.Fatal("ANIM chunk should be detected as animated WebP")
	}
	if _, err := compressWebP(animated, 1024); err == nil {
		t.Fatal("animated WebP should be rejected")
	}
}

func TestCompressWebPRejectsOtherFormats(t *testing.T) {
	if _, err := compressWebP([]byte("not-webp"), 1024); err == nil {
		t.Fatal("non-WebP input should be rejected")
	}
}

func TestWhiteBackgroundCompositesTransparency(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	src.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 128})
	composited := whiteBackground(src)
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, composited, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := decoded.At(0, 0).RGBA()
	if r < 60000 || g < 28000 || b < 28000 {
		t.Fatalf("transparent red was not composited onto white: r=%d g=%d b=%d", r, g, b)
	}
}

func TestLiveUpdateKeyIsolatesTargets(t *testing.T) {
	base := config.WebhookTarget{
		Type:              "feishu",
		AppID:             "cli_a",
		ReceiveOpenID:     "ou_user",
		GameEndTemplateID: "template_a",
	}
	otherApp := base
	otherApp.AppID = "cli_b"
	otherTemplate := base
	otherTemplate.GameEndTemplateID = "template_b"

	baseKey := liveUpdateKey("ROOM", base)
	if baseKey == liveUpdateKey("ROOM", otherApp) {
		t.Fatal("different apps must not share live-update state")
	}
	if baseKey == liveUpdateKey("ROOM", otherTemplate) {
		t.Fatal("different templates must not share live-update state")
	}
}

func TestImageCacheKeyIsolatesApps(t *testing.T) {
	cache := map[feishuImageCacheKey]string{
		{appID: "cli_a", hash: "same-hash"}: "image_a",
		{appID: "cli_b", hash: "same-hash"}: "image_b",
	}
	if len(cache) != 2 || cache[feishuImageCacheKey{appID: "cli_b", hash: "same-hash"}] != "image_b" {
		t.Fatalf("image cache keys are not app-scoped: %+v", cache)
	}
}

func TestTemplateOverridesIncludeVersion(t *testing.T) {
	target := config.WebhookTarget{
		TemplateID:             "start-id",
		TemplateVersion:        "2.0.0",
		GameEndTemplateID:      "end-id",
		GameEndTemplateVersion: "3.0.0",
	}
	startID, startVersion := gameStartTemplate(target)
	endID, endVersion := gameEndTemplate(target)
	if startID != "start-id" || startVersion != "2.0.0" || endID != "end-id" || endVersion != "3.0.0" {
		t.Fatalf("unexpected template selection: start=%s@%s end=%s@%s", startID, startVersion, endID, endVersion)
	}

	var envelope struct {
		Data struct {
			TemplateID      string `json:"template_id"`
			TemplateVersion string `json:"template_version_name"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(feishuTemplateContentRaw(endID, endVersion, nil)), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.TemplateID != "end-id" || envelope.Data.TemplateVersion != "3.0.0" {
		t.Fatalf("unexpected template envelope: %+v", envelope.Data)
	}
}

func TestFinishLiveUpdateKeepsRetryableFailure(t *testing.T) {
	f := NewFeishu(http.DefaultClient, nil, nil)
	key := liveUpdateKey("ROOM", config.WebhookTarget{AppID: "cli", ReceiveOpenID: "ou"})
	f.msgIDs[key] = &liveUpdateEntry{messageID: "message"}

	f.finishLiveUpdate(key, false, true)
	if f.msgIDs[key] == nil {
		t.Fatal("retryable failure must retain live-update state")
	}
	f.finishLiveUpdate(key, true, false)
	if f.msgIDs[key] != nil {
		t.Fatal("successful final update must clear live-update state")
	}
}

func TestFinishLiveUpdateClearsPermanentFailure(t *testing.T) {
	f := NewFeishu(http.DefaultClient, nil, nil)
	key := liveUpdateKey("ROOM", config.WebhookTarget{AppID: "cli", ReceiveOpenID: "ou"})
	f.msgIDs[key] = &liveUpdateEntry{messageID: "message"}

	f.finishLiveUpdate(key, false, false)
	if f.msgIDs[key] != nil {
		t.Fatal("non-retryable failure must clear live-update state")
	}
}
