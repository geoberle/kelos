package options

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestCompleteLoadsGitHubAppCredential(t *testing.T) {
	generated, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	key := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(generated)})
	tests := []struct {
		name    string
		files   map[string]string
		wantErr bool
	}{
		{
			name:  "complete credential",
			files: map[string]string{"appID": "5781448\n", "installationID": "182501123\n", "privateKey": string(key)},
		},
		{
			name:    "missing private key",
			files:   map[string]string{"appID": "5781448", "installationID": "182501123"},
			wantErr: true,
		},
		{
			name:    "missing installation ID",
			files:   map[string]string{"appID": "5781448", "privateKey": string(key)},
			wantErr: true,
		},
		{
			name:    "app ID is not a number",
			files:   map[string]string{"appID": "hearthctl-kelos-controller", "installationID": "182501123", "privateKey": string(key)},
			wantErr: true,
		},
		{
			name:    "private key is not a PEM key",
			files:   map[string]string{"appID": "5781448", "installationID": "182501123", "privateKey": "ghp_token"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			config := filepath.Join(dir, "config.json")
			if err := os.WriteFile(config, []byte(`{"version":"v1","repositories":["hearthctl/pi5-bootlab"],"pollInterval":"1m"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			validated, err := (&RawOptions{ConfigFile: config, AppCredentialDir: dir}).Validate()
			if err != nil {
				t.Fatal(err)
			}
			completed, err := validated.Complete()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Complete() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && completed.client == nil {
				t.Fatal("Complete() returned no GitHub client")
			}
		})
	}
}
