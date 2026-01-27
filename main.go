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

  // First, expand all file URLs and extract source paths
  var fileInfos []fileInfo

  for _, fileURL := range cfg.Files {
    expandedURL := expandEnvVars(fileURL)
    var sourcePath, shortName string

    if strings.HasPrefix(expandedURL, "http://") || strings.HasPrefix(expandedURL, "https://") {
      // For URLs, extract path after the domain
      parts := strings.Split(expandedURL, "/")
      shortName = parts[len(parts)-1]
      // Use path parts after protocol and domain (skip first 3: "", "", "domain")
      if len(parts) > 3 {
        sourcePath = strings.Join(parts[3:], "/")
      } else {
        sourcePath = shortName
      }
    } else {
      // For local files, use the file path
      shortName = filepath.Base(expandedURL)
      sourcePath = filepath.ToSlash(expandedURL)
    }

    fileInfos = append(fileInfos, fileInfo{
      originalURL: fileURL,
      expandedURL: expandedURL,
      sourcePath:  sourcePath,
      shortName:   shortName,
    })
  }

  // Calculate unique relative paths for each file
  uniquePaths := resolveUniquePaths(fileInfos)

  // Now download/copy files using the unique paths
  type embedInfo struct {
    relEmbedPath string
    uniquePath   string
  }
  var embedInfos []embedInfo

  for i, fi := range fileInfos {
    uniquePath := uniquePaths[i]
    outPath := strings.ReplaceAll(outDir, "<short_name>", strings.TrimSuffix(fi.shortName, filepath.Ext(fi.shortName)))

    // Build the full output path including unique subdirectories
    var fullOutPath string
    if uniquePath != fi.shortName {
      // There's a unique prefix path to add
      fullOutPath = filepath.Join(outPath, filepath.Dir(uniquePath))
    } else {
      fullOutPath = outPath
    }

    absOutPath := filepath.Join(cwd, fullOutPath)
    if err := os.MkdirAll(absOutPath, 0755); err != nil {
      fmt.Fprintf(os.Stderr, "failed to create dir %s: %v\n", absOutPath, err)
      os.Exit(1)
    }

    localFile := filepath.Join(absOutPath, fi.shortName)

    if strings.HasPrefix(fi.expandedURL, "http://") || strings.HasPrefix(fi.expandedURL, "https://") {
      req, err := http.NewRequest("GET", fi.expandedURL, nil)
      if err != nil {
        fmt.Fprintf(os.Stderr, "failed to create request for %s: %v\n", fi.expandedURL, err)
        os.Exit(1)
      }
      if cfg.GithubToken != "" && (strings.Contains(fi.expandedURL, "github.com") || strings.Contains(fi.expandedURL, "githubusercontent.com")) {
        req.Header.Set("Authorization", "Bearer "+cfg.GithubToken)
      }
      resp, err := http.DefaultClient.Do(req)
      if err != nil {
        fmt.Fprintf(os.Stderr, "failed to download %s: %v\n", fi.expandedURL, err)
        os.Exit(1)
      }
      defer resp.Body.Close()
      if resp.StatusCode != 200 {
        fmt.Fprintf(os.Stderr, "failed to download %s: %s\n", fi.expandedURL, resp.Status)
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
      srcFile := filepath.Join(cwd, fi.expandedURL)
      src, err := os.Open(srcFile)
      if err != nil {
        fmt.Fprintf(os.Stderr, "failed to open source file %s: %v\n", srcFile, err)
        os.Exit(1)
      }
      defer src.Close()
      dst, err := os.Create(localFile)
      if err != nil {
        fmt.Fprintf(os.Stderr, "failed to create destination file %s: %v\n", localFile, err)
        os.Exit(1)
      }
      _, err = io.Copy(dst, src)
      dst.Close()
      if err != nil {
        fmt.Fprintf(os.Stderr, "failed to copy file to %s: %v\n", localFile, err)
        os.Exit(1)
      }
    }

    // Calculate relative embed path
    fullPath := filepath.Join(fullOutPath, fi.shortName)
    goOutputDir := filepath.Dir(cfg.GoOutput)
    relEmbedPath := fullPath
    if goOutputDir != "." && goOutputDir != "" {
      relEmbedPath, _ = filepath.Rel(goOutputDir, fullPath)
    }
    relEmbedPath = filepath.ToSlash(relEmbedPath)
    embedInfos = append(embedInfos, embedInfo{relEmbedPath: relEmbedPath, uniquePath: uniquePath})
  }

  // Generate variable names from unique paths
  var embedVars []string
  for _, info := range embedInfos {
    varName := toPascalCase(strings.TrimSuffix(info.uniquePath, filepath.Ext(info.uniquePath)))
    if cfg.VarNaming == "snake" {
      varName = toGoVarName(info.uniquePath, "snake")
    }
    embedVars = append(embedVars, fmt.Sprintf("//go:embed %s\nvar %s string\n", info.relEmbedPath, varName))
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
  return toPascalCase(name)
}

// toPascalCase converts a string to PascalCase
func toPascalCase(name string) string {
  var parts []string
  current := ""
  for _, r := range name {
    if r == '-' || r == '_' || r == '.' || r == '/' {
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

// fileInfo holds information about a file to be embedded
type fileInfo struct {
  originalURL string
  expandedURL string
  sourcePath  string // path portion for uniqueness calculation
  shortName   string
}

// resolveUniquePaths takes file infos and returns the minimum unique path for each file
// by including parent directory parts from the right until all paths are unique
func resolveUniquePaths(files []fileInfo) []string {
  result := make([]string, len(files))

  // Count occurrences of each filename
  nameCount := make(map[string][]int)
  for i, f := range files {
    nameCount[f.shortName] = append(nameCount[f.shortName], i)
  }

  for i, f := range files {
    if len(nameCount[f.shortName]) == 1 {
      // Unique filename, just use the filename
      result[i] = f.shortName
    } else {
      // Need to find minimum unique path from right
      pathParts := strings.Split(f.sourcePath, "/")

      // Try increasing depths until we find a unique path
      for depth := 1; depth <= len(pathParts); depth++ {
        startIdx := len(pathParts) - depth
        if startIdx < 0 {
          startIdx = 0
        }
        candidatePath := strings.Join(pathParts[startIdx:], "/")

        // Check if this path is unique among files with same shortName
        isUnique := true
        for _, otherIdx := range nameCount[f.shortName] {
          if otherIdx == i {
            continue
          }
          otherParts := strings.Split(files[otherIdx].sourcePath, "/")
          otherStartIdx := len(otherParts) - depth
          if otherStartIdx < 0 {
            otherStartIdx = 0
          }
          otherPath := strings.Join(otherParts[otherStartIdx:], "/")
          if otherPath == candidatePath {
            isUnique = false
            break
          }
        }

        if isUnique {
          result[i] = candidatePath
          break
        }
      }

      // Fallback to full path if nothing is unique
      if result[i] == "" {
        result[i] = f.sourcePath
      }
    }
  }

  return result
}

// resolveUniqueVarNames takes a list of embed paths and returns unique variable names
// by including parent directory parts when there are duplicates
func resolveUniqueVarNames(paths []string, naming string) []string {
  // First pass: get base var names and detect duplicates
  baseNames := make([]string, len(paths))
  nameToIndices := make(map[string][]int)

  for i, p := range paths {
    baseName := filepath.Base(p)
    varName := toGoVarName(baseName, naming)
    baseNames[i] = varName
    nameToIndices[varName] = append(nameToIndices[varName], i)
  }

  // Second pass: for duplicates, find minimum depth that makes all unique
  result := make([]string, len(paths))

  for i, p := range paths {
    varName := baseNames[i]
    indices := nameToIndices[varName]

    if len(indices) > 1 {
      // Need to make unique - find minimum depth where this path differs from all others
      pathParts := strings.Split(filepath.ToSlash(p), "/")

      for depth := 2; depth <= len(pathParts); depth++ {
        startIdx := len(pathParts) - depth
        if startIdx < 0 {
          startIdx = 0
        }
        relevantParts := make([]string, len(pathParts[startIdx:]))
        copy(relevantParts, pathParts[startIdx:])

        // Build var name from path parts (excluding extension from last part)
        lastPart := relevantParts[len(relevantParts)-1]
        lastPart = strings.TrimSuffix(lastPart, filepath.Ext(lastPart))
        relevantParts[len(relevantParts)-1] = lastPart

        var candidate string
        if naming == "snake" {
          // For snake case: Title only the prefix parts, keep base name lowercase with underscores
          var prefixParts []string
          for j := 0; j < len(relevantParts)-1; j++ {
            prefixParts = append(prefixParts, strings.Title(relevantParts[j]))
          }
          // Base part: replace - and . with _, keep lowercase
          basePart := relevantParts[len(relevantParts)-1]
          basePart = strings.ReplaceAll(basePart, "-", "_")
          basePart = strings.ReplaceAll(basePart, ".", "_")
          if len(prefixParts) > 0 {
            candidate = strings.Join(prefixParts, "_") + "_" + basePart
          } else {
            candidate = strings.Title(basePart)
          }
        } else {
          // For pascal case: use toPascalCase
          candidate = toPascalCase(strings.Join(relevantParts, "/"))
        }

        // Check if this candidate is unique among all paths with same base name
        isUnique := true
        for _, otherIdx := range indices {
          if otherIdx == i {
            continue
          }
          otherParts := strings.Split(filepath.ToSlash(paths[otherIdx]), "/")
          otherStartIdx := len(otherParts) - depth
          if otherStartIdx < 0 {
            otherStartIdx = 0
          }
          otherRelevantParts := make([]string, len(otherParts[otherStartIdx:]))
          copy(otherRelevantParts, otherParts[otherStartIdx:])
          otherLastPart := otherRelevantParts[len(otherRelevantParts)-1]
          otherLastPart = strings.TrimSuffix(otherLastPart, filepath.Ext(otherLastPart))
          otherRelevantParts[len(otherRelevantParts)-1] = otherLastPart

          var otherCandidate string
          if naming == "snake" {
            var prefixParts []string
            for j := 0; j < len(otherRelevantParts)-1; j++ {
              prefixParts = append(prefixParts, strings.Title(otherRelevantParts[j]))
            }
            basePart := otherRelevantParts[len(otherRelevantParts)-1]
            basePart = strings.ReplaceAll(basePart, "-", "_")
            basePart = strings.ReplaceAll(basePart, ".", "_")
            if len(prefixParts) > 0 {
              otherCandidate = strings.Join(prefixParts, "_") + "_" + basePart
            } else {
              otherCandidate = strings.Title(basePart)
            }
          } else {
            otherCandidate = toPascalCase(strings.Join(otherRelevantParts, "/"))
          }

          if otherCandidate == candidate {
            isUnique = false
            break
          }
        }

        if isUnique {
          varName = candidate
          break
        }
      }
    }

    result[i] = varName
  }

  return result
}
