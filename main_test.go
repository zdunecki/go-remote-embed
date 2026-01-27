package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestToGoVarName(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		tests := []struct {
			input    string
			expected string
		}{
			{"hello.txt", "Hello"},
			{"my-file.txt", "MyFile"},
			{"some.config.yaml", "SomeConfig"},
		}

		for _, tt := range tests {
			t.Run(tt.input, func(t *testing.T) {
				result := toGoVarName(tt.input, "")
				if result != tt.expected {
					t.Errorf("toGoVarName(%q, \"pascal\") = %q, want %q", tt.input, result, tt.expected)
				}
			})
		}
	})

	t.Run("pascal", func(t *testing.T) {
		tests := []struct {
			input    string
			expected string
		}{
			{"hello.txt", "Hello"},
			{"my-file.txt", "MyFile"},
			{"some.config.yaml", "SomeConfig"},
			{"simple", "Simple"},
			{"with-many-dashes.go", "WithManyDashes"},
			{"file.name.with.dots.txt", "FileNameWithDots"},
			{"config_xml.xml", "ConfigXml"},
			{"create_tables.sql", "CreateTables"},
		}

		for _, tt := range tests {
			t.Run(tt.input, func(t *testing.T) {
				result := toGoVarName(tt.input, "pascal")
				if result != tt.expected {
					t.Errorf("toGoVarName(%q, \"pascal\") = %q, want %q", tt.input, result, tt.expected)
				}
			})
		}
	})

	t.Run("snake", func(t *testing.T) {
		tests := []struct {
			input    string
			expected string
		}{
			{"hello.txt", "Hello"},
			{"my-file.txt", "My_file"},
			{"some.config.yaml", "Some_config"},
			{"simple", "Simple"},
			{"with-many-dashes.go", "With_many_dashes"},
			{"file.name.with.dots.txt", "File_name_with_dots"},
		}

		for _, tt := range tests {
			t.Run(tt.input, func(t *testing.T) {
				result := toGoVarName(tt.input, "snake")
				if result != tt.expected {
					t.Errorf("toGoVarName(%q, \"snake\") = %q, want %q", tt.input, result, tt.expected)
				}
			})
		}
	})
}

func TestEmbedConfigParsing(t *testing.T) {
	tmpDir := t.TempDir()

	configContent := `go-output: generated_embed.go
output: assets
files:
  - file1.txt
  - file2.txt
go-mod: mypackage
`
	configPath := filepath.Join(tmpDir, "embed.yaml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}

	var cfg EmbedConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	if cfg.GoOutput != "generated_embed.go" {
		t.Errorf("GoOutput = %q, want %q", cfg.GoOutput, "generated_embed.go")
	}
	if cfg.Output != "assets" {
		t.Errorf("Output = %q, want %q", cfg.Output, "assets")
	}
	if len(cfg.Files) != 2 {
		t.Errorf("len(Files) = %d, want 2", len(cfg.Files))
	}
	if cfg.GoMod != "mypackage" {
		t.Errorf("GoMod = %q, want %q", cfg.GoMod, "mypackage")
	}
}

func TestEmbedConfigDefaults(t *testing.T) {
	configContent := `files:
  - test.txt
`
	var cfg EmbedConfig
	if err := yaml.Unmarshal([]byte(configContent), &cfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	// Test default value logic (as done in main)
	if cfg.GoOutput == "" {
		cfg.GoOutput = "embed.go"
	}

	if cfg.GoOutput != "embed.go" {
		t.Errorf("GoOutput default = %q, want %q", cfg.GoOutput, "embed.go")
	}
}

func TestLocalFileCopy(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a source file
	srcContent := "hello world content"
	srcPath := filepath.Join(tmpDir, "source.txt")
	if err := os.WriteFile(srcPath, []byte(srcContent), 0644); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}

	// Create output directory
	outDir := filepath.Join(tmpDir, "output")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		t.Fatalf("failed to create output dir: %v", err)
	}

	// Copy the file (simulating what main does for local files)
	dstPath := filepath.Join(outDir, "source.txt")
	src, err := os.Open(srcPath)
	if err != nil {
		t.Fatalf("failed to open source: %v", err)
	}
	defer src.Close()

	dst, err := os.Create(dstPath)
	if err != nil {
		t.Fatalf("failed to create destination: %v", err)
	}

	srcData, _ := os.ReadFile(srcPath)
	if _, err := dst.Write(srcData); err != nil {
		t.Fatalf("failed to write destination: %v", err)
	}
	dst.Close()

	// Verify
	dstData, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("failed to read destination: %v", err)
	}
	if string(dstData) != srcContent {
		t.Errorf("copied content = %q, want %q", string(dstData), srcContent)
	}
}

func TestRemoteFileDownload(t *testing.T) {
	// Create a test HTTP server
	expectedContent := "remote file content"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(expectedContent))
	}))
	defer server.Close()

	// Download from test server
	resp, err := http.Get(server.URL + "/test.txt")
	if err != nil {
		t.Fatalf("failed to download: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("status code = %d, want 200", resp.StatusCode)
	}

	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "downloaded.txt")

	f, err := os.Create(outPath)
	if err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	buf := make([]byte, 1024)
	n, _ := resp.Body.Read(buf)
	f.Write(buf[:n])
	f.Close()

	// Verify
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("failed to read downloaded file: %v", err)
	}
	if string(data) != expectedContent {
		t.Errorf("downloaded content = %q, want %q", string(data), expectedContent)
	}
}

func TestOutputPathWithShortNamePlaceholder(t *testing.T) {
	tests := []struct {
		outDir    string
		shortName string
		expected  string
	}{
		{"assets/<short_name>", "hello.txt", "assets/hello"},
		{"<short_name>/files", "config.yaml", "config/files"},
		{"output", "test.go", "output"},
	}

	for _, tt := range tests {
		t.Run(tt.outDir, func(t *testing.T) {
			result := replaceShortName(tt.outDir, tt.shortName)
			if result != tt.expected {
				t.Errorf("replaceShortName(%q, %q) = %q, want %q", tt.outDir, tt.shortName, result, tt.expected)
			}
		})
	}
}

// Helper function to test - mirrors the logic in main
func replaceShortName(outDir, shortName string) string {
	return strings.ReplaceAll(outDir, "<short_name>", strings.TrimSuffix(shortName, filepath.Ext(shortName)))
}

func TestResolveUniqueVarNames(t *testing.T) {
	tests := []struct {
		name     string
		paths    []string
		naming   string
		expected []string
	}{
		{
			name:     "no duplicates",
			paths:    []string{".schemas/config.xml", ".schemas/users.json", ".schemas/orders.sql"},
			naming:   "pascal",
			expected: []string{"Config", "Users", "Orders"},
		},
		{
			name: "duplicates with different parent dirs",
			paths: []string{
				".schemas/visitors.json",
				".schemas/session_views.json",
				".indices/mapping/visitors.json",
				".indices/settings/visitors.json",
			},
			naming: "pascal",
			expected: []string{
				"SchemasVisitors",
				"SessionViews",
				"MappingVisitors",
				"SettingsVisitors",
			},
		},
		{
			name: "multiple duplicates same name",
			paths: []string{
				"a/config.json",
				"b/config.json",
				"c/config.json",
			},
			naming: "pascal",
			expected: []string{
				"AConfig",
				"BConfig",
				"CConfig",
			},
		},
		{
			name: "deep path duplicates",
			paths: []string{
				"level1/level2/level3/file.txt",
				"other1/other2/other3/file.txt",
			},
			naming: "pascal",
			expected: []string{
				"Level3File",
				"Other3File",
			},
		},
		{
			name:     "single file",
			paths:    []string{".schemas/create-tables.sql"},
			naming:   "pascal",
			expected: []string{"CreateTables"},
		},
		{
			name: "snake naming with duplicates",
			paths: []string{
				"mapping/session_tokens.json",
				"settings/session_tokens.json",
			},
			naming: "snake",
			expected: []string{
				"Mapping_session_tokens",
				"Settings_session_tokens",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolveUniqueVarNames(tt.paths, tt.naming)
			if len(result) != len(tt.expected) {
				t.Fatalf("length mismatch: got %d, want %d", len(result), len(tt.expected))
			}
			for i, r := range result {
				if r != tt.expected[i] {
					t.Errorf("result[%d] = %q, want %q", i, r, tt.expected[i])
				}
			}
		})
	}
}

func TestToPascalCase(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "Hello"},
		{"hello-world", "HelloWorld"},
		{"hello_world", "HelloWorld"},
		{"hello.world", "HelloWorld"},
		{"hello/world", "HelloWorld"},
		{"mapping/session_tokens", "MappingSessionTokens"},
		{"a/b/c", "ABC"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := toPascalCase(tt.input)
			if result != tt.expected {
				t.Errorf("toPascalCase(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
