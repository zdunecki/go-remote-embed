package main

import (
  "bufio"
  "fmt"
  "io"
  "net/http"
  "os"
  "path/filepath"
  "strings"
  "gopkg.in/yaml.v3"
)

var envVars = make(map[string]string)

type EmbedConfig struct {
  GoOutput    string   `yaml:"go-output"`
  Output      string   `yaml:"output"`
  Files       []string `yaml:"files"`
  GoMod       string   `yaml:"go-mod"`
  GithubToken string   `yaml:"github-token"`
  VarNaming   string   `yaml:"var-naming"` // "pascal" (default) or "snake"
}

func main() {
  // 1. Read embed.yaml in current directory (for use from examples/basic)
  cwd, _ := os.Getwd()

  // Load .env file if present
  loadDotEnv(cwd)

  configPath := filepath.Join(cwd, "embed.yaml")
  if _, err := os.Stat(configPath); os.IsNotExist(err) {
    fmt.Fprintln(os.Stderr, "embed.yaml not found in current directory")
    os.Exit(1)
  }
  configData, err := os.ReadFile(configPath)
  if err != nil {
    fmt.Fprintf(os.Stderr, "failed to read %s: %v\n", configPath, err)
    os.Exit(1)
  }
  var cfg EmbedConfig
  if err := yaml.Unmarshal(configData, &cfg); err != nil {
    fmt.Fprintf(os.Stderr, "failed to parse %s: %v\n", configPath, err)
    os.Exit(1)
  }
  if cfg.GoOutput == "" {
    cfg.GoOutput = "embed.go"
  }
  if cfg.GithubToken != "" {
    cfg.GithubToken = expandEnvVars(cfg.GithubToken)
  }
  if len(cfg.Files) == 0 {
    fmt.Fprintln(os.Stderr, "No files specified in embed.yaml")
    os.Exit(1)
  }

  // 2. Download files and write to output dir (relative to cwd)
  outDir := cfg.Output
  if outDir == "" {
    outDir = "."
  }

  var embedVars []string
  for _, fileURL := range cfg.Files {
    // Expand environment variables in file URL
    fileURL = expandEnvVars(fileURL)
    var shortName string
    var outPath string
    var absOutPath string
    if strings.HasPrefix(fileURL, "http://") || strings.HasPrefix(fileURL, "https://") {
      parts := strings.Split(fileURL, "/")
      shortName = parts[len(parts)-1]
      outPath = strings.ReplaceAll(outDir, "<short_name>", strings.TrimSuffix(shortName, filepath.Ext(shortName)))
      absOutPath = filepath.Join(cwd, outPath)
      if err := os.MkdirAll(absOutPath, 0755); err != nil {
        fmt.Fprintf(os.Stderr, "failed to create dir %s: %v\n", absOutPath, err)
        os.Exit(1)
      }
      localFile := filepath.Join(absOutPath, shortName)
      req, err := http.NewRequest("GET", fileURL, nil)
      if err != nil {
        fmt.Fprintf(os.Stderr, "failed to create request for %s: %v\n", fileURL, err)
        os.Exit(1)
      }
      if cfg.GithubToken != "" && strings.Contains(fileURL, "github.com") {
        req.Header.Set("Authorization", "token "+cfg.GithubToken)
      }
      resp, err := http.DefaultClient.Do(req)
      if err != nil {
        fmt.Fprintf(os.Stderr, "failed to download %s: %v\n", fileURL, err)
        os.Exit(1)
      }
      defer resp.Body.Close()
      if resp.StatusCode != 200 {
        fmt.Fprintf(os.Stderr, "failed to download %s: %s\n", fileURL, resp.Status)
        os.Exit(1)
      }
      f, err := os.Create(localFile)
      if err != nil {
        fmt.Fprintf(os.Stderr, "failed to create file %s: %v\n", localFile, err)
        os.Exit(1)
      }
      _, err = io.Copy(f, resp.Body)
      f.Close()
      if err != nil {
        fmt.Fprintf(os.Stderr, "failed to write file %s: %v\n", localFile, err)
        os.Exit(1)
      }
    } else {
      // Treat as local file path
      shortName = filepath.Base(fileURL)
      outPath = strings.ReplaceAll(outDir, "<short_name>", strings.TrimSuffix(shortName, filepath.Ext(shortName)))
      absOutPath = filepath.Join(cwd, outPath)
      if err := os.MkdirAll(absOutPath, 0755); err != nil {
        fmt.Fprintf(os.Stderr, "failed to create dir %s: %v\n", absOutPath, err)
        os.Exit(1)
      }
      srcFile := filepath.Join(cwd, fileURL)
      dstFile := filepath.Join(absOutPath, shortName)
      src, err := os.Open(srcFile)
      if err != nil {
        fmt.Fprintf(os.Stderr, "failed to open source file %s: %v\n", srcFile, err)
        os.Exit(1)
      }
      defer src.Close()
      dst, err := os.Create(dstFile)
      if err != nil {
        fmt.Fprintf(os.Stderr, "failed to create destination file %s: %v\n", dstFile, err)
        os.Exit(1)
      }
      _, err = io.Copy(dst, src)
      dst.Close()
      if err != nil {
        fmt.Fprintf(os.Stderr, "failed to copy file to %s: %v\n", dstFile, err)
        os.Exit(1)
      }
    }
    varName := toGoVarName(shortName, cfg.VarNaming)
    // Use relative path for go:embed
    relEmbedPath := filepath.ToSlash(filepath.Join(outPath, shortName))
    embedVars = append(embedVars, fmt.Sprintf("//go:embed %s\nvar %s string\n", relEmbedPath, varName))
  }

  // 3. Detect package name
  pkgName := "main"
  if strings.TrimSpace(cfg.GoMod) != "" {
    pkgName = strings.TrimSpace(cfg.GoMod)
  } else {
    // Try go.mod first
    gomodPath := filepath.Join(cwd, "go.mod")
    if data, err := os.ReadFile(gomodPath); err == nil {
      lines := strings.Split(string(data), "\n")
      for _, l := range lines {
        l = strings.TrimSpace(l)
        if strings.HasPrefix(l, "module ") {
          parts := strings.Split(l, "/")
          pkgName = parts[len(parts)-1]
          pkgName = strings.ReplaceAll(pkgName, "-", "_")
          break
        }
      }
    } else {
      // Scan all .go files in cwd for package name
      entries, err := os.ReadDir(cwd)
      if err == nil {
        pkgCount := map[string]int{}
        for _, entry := range entries {
          // Only consider .go files that are not embed.go and not generated (e.g., only main.go)
          if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".go") && entry.Name() != cfg.GoOutput && entry.Name() != "embed.go" {
            filePath := filepath.Join(cwd, entry.Name())
            data, err := os.ReadFile(filePath)
            if err == nil {
              lines := strings.Split(string(data), "\n")
              for _, l := range lines {
                l = strings.TrimSpace(l)
                if strings.HasPrefix(l, "package ") {
                  name := strings.TrimPrefix(l, "package ")
                  name = strings.Fields(name)[0]
                  pkgCount[name]++
                  break
                }
              }
            }
          }
        }
        // Use the most common package name
        maxCount := 0
        for name, count := range pkgCount {
          if count > maxCount {
            pkgName = name
            maxCount = count
          }
        }
      }
    }
  }

  // 4. Generate embed.go in cwd
  embedGo := fmt.Sprintf("package %s\n\nimport (\n\t_ \"embed\"\n)\n\n// Embedded assets generated by remoteembed\n\n", pkgName)
  for _, v := range embedVars {
    embedGo += v + "\n"
  }
  embedGoPath := filepath.Join(cwd, cfg.GoOutput)
  if err := os.WriteFile(embedGoPath, []byte(embedGo), 0644); err != nil {
    fmt.Fprintf(os.Stderr, "failed to write %s: %v\n", embedGoPath, err)
    os.Exit(1)
  }
}

// loadDotEnv loads environment variables from a .env file if it exists
func loadDotEnv(dir string) {
  envPath := filepath.Join(dir, ".env")
  f, err := os.Open(envPath)
  if err != nil {
    return
  }
  defer f.Close()
  scanner := bufio.NewScanner(f)
  for scanner.Scan() {
    line := strings.TrimSpace(scanner.Text())
    if line == "" || strings.HasPrefix(line, "#") {
      continue
    }
    parts := strings.SplitN(line, "=", 2)
    if len(parts) != 2 {
      continue
    }
    key := strings.TrimSpace(parts[0])
    value := strings.TrimSpace(parts[1])
    // Remove surrounding quotes if present
    if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
      value = value[1 : len(value)-1]
    }
    envVars[key] = value
  }
}

// getEnv returns the value of an environment variable, checking .env first then os.Getenv
func getEnv(key string) string {
  if val, ok := envVars[key]; ok {
    return val
  }
  return os.Getenv(key)
}

// expandEnvVars expands environment variables in the format $VAR or ${VAR}
func expandEnvVars(s string) string {
  return os.Expand(s, getEnv)
}

// toGoVarName converts a file name to a Go exported variable name
// naming: "pascal" (default) -> PascalCase, "snake" -> Snake_Case
func toGoVarName(name string, naming string) string {
  name = strings.TrimSuffix(name, filepath.Ext(name))
  if naming == "snake" {
    name = strings.ReplaceAll(name, "-", "_")
    name = strings.ReplaceAll(name, ".", "_")
    return strings.Title(name)
  }
  // Default: PascalCase
  var parts []string
  current := ""
  for _, r := range name {
    if r == '-' || r == '_' || r == '.' {
      if current != "" {
        parts = append(parts, current)
        current = ""
      }
    } else {
      current += string(r)
    }
  }
  if current != "" {
    parts = append(parts, current)
  }
  var result string
  for _, part := range parts {
    result += strings.Title(strings.ToLower(part))
  }
  return result
}
