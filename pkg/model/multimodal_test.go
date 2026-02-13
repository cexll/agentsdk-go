package model

import (
	"testing"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
)

func TestConvertContentBlocks_TextOnly(t *testing.T) {
	blocks := []ContentBlock{
		{Type: ContentBlockText, Text: "hello"},
	}
	result := convertContentBlocks(blocks)
	if len(result) != 1 {
		t.Fatalf("expected 1 block, got %d", len(result))
	}
	if text := result[0].GetText(); text == nil || *text != "hello" {
		t.Fatalf("expected text 'hello', got %v", text)
	}
}

func TestConvertContentBlocks_ImageBase64(t *testing.T) {
	blocks := []ContentBlock{
		{Type: ContentBlockImage, MediaType: "image/png", Data: "iVBOR..."},
	}
	result := convertContentBlocks(blocks)
	if len(result) != 1 {
		t.Fatalf("expected 1 block, got %d", len(result))
	}
	if result[0].OfImage == nil {
		t.Fatal("expected image block")
	}
	src := result[0].OfImage.Source.OfBase64
	if src == nil {
		t.Fatal("expected base64 image source")
	}
	if src.Data != "iVBOR..." {
		t.Fatalf("expected data 'iVBOR...', got %q", src.Data)
	}
	if string(src.MediaType) != "image/png" {
		t.Fatalf("expected media type 'image/png', got %q", src.MediaType)
	}
}

func TestConvertContentBlocks_ImageURL(t *testing.T) {
	blocks := []ContentBlock{
		{Type: ContentBlockImage, URL: "https://example.com/img.png"},
	}
	result := convertContentBlocks(blocks)
	if len(result) != 1 {
		t.Fatalf("expected 1 block, got %d", len(result))
	}
	if result[0].OfImage == nil {
		t.Fatal("expected image block")
	}
	src := result[0].OfImage.Source.OfURL
	if src == nil {
		t.Fatal("expected URL image source")
	}
	if src.URL != "https://example.com/img.png" {
		t.Fatalf("expected URL 'https://example.com/img.png', got %q", src.URL)
	}
}

func TestConvertContentBlocks_Document(t *testing.T) {
	blocks := []ContentBlock{
		{Type: ContentBlockDocument, MediaType: "application/pdf", Data: "JVBERi0..."},
	}
	result := convertContentBlocks(blocks)
	if len(result) != 1 {
		t.Fatalf("expected 1 block, got %d", len(result))
	}
	if result[0].OfDocument == nil {
		t.Fatal("expected document block")
	}
	src := result[0].OfDocument.Source.OfBase64
	if src == nil {
		t.Fatal("expected base64 PDF source")
	}
	if src.Data != "JVBERi0..." {
		t.Fatalf("expected data 'JVBERi0...', got %q", src.Data)
	}
}

func TestConvertContentBlocks_Mixed(t *testing.T) {
	blocks := []ContentBlock{
		{Type: ContentBlockText, Text: "Describe this:"},
		{Type: ContentBlockImage, MediaType: "image/jpeg", Data: "/9j/4AAQ..."},
		{Type: ContentBlockText, Text: "And this:"},
		{Type: ContentBlockDocument, MediaType: "application/pdf", Data: "JVBERi0..."},
	}
	result := convertContentBlocks(blocks)
	if len(result) != 4 {
		t.Fatalf("expected 4 blocks, got %d", len(result))
	}
	if text := result[0].GetText(); text == nil || *text != "Describe this:" {
		t.Fatalf("block 0: expected text, got %v", text)
	}
	if result[1].OfImage == nil {
		t.Fatal("block 1: expected image")
	}
	if text := result[2].GetText(); text == nil || *text != "And this:" {
		t.Fatalf("block 2: expected text, got %v", text)
	}
	if result[3].OfDocument == nil {
		t.Fatal("block 3: expected document")
	}
}

func TestConvertContentBlocks_Empty(t *testing.T) {
	result := convertContentBlocks(nil)
	if len(result) != 1 {
		t.Fatalf("expected fallback block, got %d", len(result))
	}
	if text := result[0].GetText(); text == nil || *text != "." {
		t.Fatalf("expected fallback '.', got %v", text)
	}
}

func TestConvertContentBlocks_EmptyTextFallback(t *testing.T) {
	blocks := []ContentBlock{
		{Type: ContentBlockText, Text: "  "},
	}
	result := convertContentBlocks(blocks)
	if len(result) != 1 {
		t.Fatalf("expected 1 block, got %d", len(result))
	}
	if text := result[0].GetText(); text == nil || *text != "." {
		t.Fatalf("expected '.', got %v", text)
	}
}

func TestConvertMessages_UserWithContentBlocks(t *testing.T) {
	msgs := []Message{
		{
			Role: "user",
			ContentBlocks: []ContentBlock{
				{Type: ContentBlockText, Text: "What is this?"},
				{Type: ContentBlockImage, MediaType: "image/png", Data: "base64data"},
			},
		},
	}
	_, params, err := convertMessages(msgs, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(params) != 1 {
		t.Fatalf("expected 1 message, got %d", len(params))
	}
	if params[0].Role != anthropicsdk.MessageParamRoleUser {
		t.Fatalf("expected user role, got %s", params[0].Role)
	}
	if len(params[0].Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(params[0].Content))
	}
}

func TestConvertMessages_UserContentMergedWithContentBlocks(t *testing.T) {
	// When both Content (text) and ContentBlocks (image) exist,
	// the text should be prepended as a text block alongside the content blocks.
	msgs := []Message{
		{
			Role:    "user",
			Content: "What is in this image?",
			ContentBlocks: []ContentBlock{
				{Type: ContentBlockImage, MediaType: "image/jpeg", Data: "base64data"},
			},
		},
	}
	_, params, err := convertMessages(msgs, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(params) != 1 {
		t.Fatalf("expected 1 message, got %d", len(params))
	}
	// Expect 2 blocks: text from Content + image from ContentBlocks
	if len(params[0].Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(params[0].Content))
	}
	if text := params[0].Content[0].GetText(); text == nil || *text != "What is in this image?" {
		t.Fatalf("expected text block 'What is in this image?', got %v", text)
	}
}

func TestConvertMessages_UserFallsBackToContent(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "plain text"},
	}
	_, params, err := convertMessages(msgs, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(params) != 1 {
		t.Fatalf("expected 1 message, got %d", len(params))
	}
	if len(params[0].Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(params[0].Content))
	}
	if text := params[0].Content[0].GetText(); text == nil || *text != "plain text" {
		t.Fatalf("expected 'plain text', got %v", text)
	}
}

func TestConvertContentBlocks_SkipsEmptyImageAndDoc(t *testing.T) {
	blocks := []ContentBlock{
		{Type: ContentBlockImage},    // no Data or URL
		{Type: ContentBlockDocument}, // no Data
	}
	result := convertContentBlocks(blocks)
	// Should fall back to "." since no valid blocks produced
	if len(result) != 1 {
		t.Fatalf("expected 1 fallback block, got %d", len(result))
	}
	if text := result[0].GetText(); text == nil || *text != "." {
		t.Fatalf("expected '.', got %v", text)
	}
}

func TestCacheControl_MultimodalMessageEndingWithImage(t *testing.T) {
	msgs := []Message{
		{
			Role: "user",
			ContentBlocks: []ContentBlock{
				{Type: ContentBlockText, Text: "Describe this image:"},
				{Type: ContentBlockImage, MediaType: "image/png", Data: "base64data"},
			},
		},
	}
	_, params, err := convertMessages(msgs, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(params) != 1 {
		t.Fatalf("expected 1 message, got %d", len(params))
	}
	// The text block (index 0) should have cache control, not the image (index 1)
	textBlock := params[0].Content[0]
	if textBlock.OfText == nil {
		t.Fatal("expected text block at index 0")
	}
	if textBlock.OfText.CacheControl.Type == "" {
		t.Fatal("expected cache control on text block when last block is image")
	}
	if textBlock.OfText.Text != "Describe this image:" {
		t.Fatalf("expected text preserved, got %q", textBlock.OfText.Text)
	}
}

func TestCacheControl_TextOnlyMessage(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hello"},
	}
	_, params, err := convertMessages(msgs, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	textBlock := params[0].Content[0]
	if textBlock.OfText == nil || textBlock.OfText.CacheControl.Type == "" {
		t.Fatal("expected cache control on text-only message")
	}
}

func TestCacheControl_AllImageMessage_NoCacheSlotWasted(t *testing.T) {
	// A message with only image blocks should not consume a cache slot
	msgs := []Message{
		{
			Role: "user",
			ContentBlocks: []ContentBlock{
				{Type: ContentBlockImage, MediaType: "image/png", Data: "img1"},
			},
		},
		{Role: "user", Content: "second message"},
	}
	_, params, err := convertMessages(msgs, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The second message (text-only) should get cache control
	if len(params) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(params))
	}
	// Second message should have cache control
	secondBlock := params[1].Content[0]
	if secondBlock.OfText == nil || secondBlock.OfText.CacheControl.Type == "" {
		t.Fatal("expected cache control on second (text) message")
	}
}

func TestConvertMessagesToOpenAI_MultimodalUsesImageParts(t *testing.T) {
	msgs := []Message{
		{
			Role:    "user",
			Content: "fallback text",
			ContentBlocks: []ContentBlock{
				{Type: ContentBlockText, Text: "visible text"},
				{Type: ContentBlockImage, MediaType: "image/png", Data: "YWJj"},
			},
		},
	}
	result := convertMessagesToOpenAI(msgs)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if result[0].OfUser == nil {
		t.Fatal("expected user message")
	}
	parts := result[0].OfUser.Content.OfArrayOfContentParts
	if len(parts) != 3 {
		t.Fatalf("expected 3 content parts (content+text+image), got %d", len(parts))
	}
	if text := parts[0].GetText(); text == nil || *text != "fallback text" {
		t.Fatalf("expected first text part 'fallback text', got %v", text)
	}
	if text := parts[1].GetText(); text == nil || *text != "visible text" {
		t.Fatalf("expected second text part 'visible text', got %v", text)
	}
	image := parts[2].GetImageURL()
	if image == nil {
		t.Fatal("expected image content part")
	}
	if image.URL != "data:image/png;base64,YWJj" {
		t.Fatalf("expected data URI image, got %q", image.URL)
	}
}

func TestConvertMessagesToOpenAI_MultimodalImageURL(t *testing.T) {
	msgs := []Message{
		{
			Role: "user",
			ContentBlocks: []ContentBlock{
				{Type: ContentBlockImage, URL: "https://example.com/a.png"},
			},
		},
	}
	result := convertMessagesToOpenAI(msgs)
	if len(result) != 1 || result[0].OfUser == nil {
		t.Fatalf("expected one user message, got %+v", result)
	}
	parts := result[0].OfUser.Content.OfArrayOfContentParts
	if len(parts) != 1 {
		t.Fatalf("expected single image part, got %d", len(parts))
	}
	image := parts[0].GetImageURL()
	if image == nil || image.URL != "https://example.com/a.png" {
		t.Fatalf("expected image URL part, got %+v", image)
	}
}
