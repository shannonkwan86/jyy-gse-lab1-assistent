package llm

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigSeparatesProvidersAndRelativePaths(t *testing.T) {
	t.Setenv("ASSISTANT_TEST_CHAT_KEY", "chat-secret")
	root := t.TempDir()
	p := filepath.Join(root, "config.json")
	os.WriteFile(p, []byte(`{"articles_dir":"posts","index_db":"index.db","chat":{"base_url":"https://chat.example/v1","model":"chat","api_key_env":"ASSISTANT_TEST_CHAT_KEY"},"embedding":{"base_url":"https://embed.example/v1","model":"embed","api_key":"local-secret"}}`), 0600)
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Chat.APIKey != "chat-secret" || c.Embedding.APIKey != "local-secret" || c.ArticlesDir != filepath.Join(root, "posts") {
		t.Fatalf("wrong provider/path configuration")
	}
}
func TestConfigFailsOnMissingCredentials(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "c.json")
	os.WriteFile(p, []byte(fmt.Sprintf(`{"articles_dir":%q,"index_db":"index.db","chat":{"base_url":"https://chat.example/v1","model":"chat","api_key":""},"embedding":{"base_url":"https://embed.example/v1","model":"embed","api_key":""}}`, root)), 0600)
	if _, err := LoadConfig(p); err == nil {
		t.Fatal("empty credentials accepted")
	}
}
