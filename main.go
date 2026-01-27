package main

import (
  "fmt"
  "io"
  "net/http"
  "os"
  "path/filepath"
  "strings"
  "gopkg.in/yaml.v3"
)

type EmbedConfig struct {
  GoOutput string   `yaml:"go-output"`
  Output  string   `yaml:"output"`
  Files   []string `yaml:"files"`
  GoMod   string   `yaml:"go-mod"`
}

func main() {
  // 1. Read embed.yaml in current directory (for use from examples/basic)
  cwd, _ := os.Getwd()
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
      resp, err := http.Get(fileURL)
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
    varName := toGoVarName(shortName)
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

// toGoVarName converts a file name to a Go exported variable name
func toGoVarName(name string) string {
  name = strings.TrimSuffix(name, filepath.Ext(name))
  name = strings.ReplaceAll(name, "-", "_")
  name = strings.ReplaceAll(name, ".", "_")
  name = strings.Title(name)
  return name
}
