package local

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
)

var (
	logFile *os.File
	// once    sync.Once
)

func getModuleName() string {
	info, ok := debug.ReadBuildInfo()
	if ok && info.Main.Path != "" {
		return path.Base(info.Main.Path)
	}
	return "cli-app-data"
}

func getConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	moduleName := getModuleName()
	configDir := filepath.Join(home, "."+moduleName)
	if _, err := os.Stat(configDir); os.IsNotExist(err) {
		if err := os.MkdirAll(configDir, 0700); err != nil {
			return "", err
		}
	}
	return configDir, nil
}

func GetSecretPath(clusterName string) (string, error) {
	dir, err := getConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("%s.secret", clusterName)), nil
}

// InitLog initializes log writing to a file
func InitLog(clusterName string) error {
	dir, err := getConfigDir()
	if err != nil {
		return err
	}

	logPath := filepath.Join(dir, fmt.Sprintf("%s.log", clusterName))

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("log file opening error: %w", err)
	}

	logFile = f
	fmt.Printf("[NOTE] Logging enabled: %s\n", logPath)

	header := fmt.Sprintf("\n--- SESSION START: %s ---\n", time.Now().Format(time.RFC3339))
	f.WriteString(header)

	return nil
}

// CloseLog closes the file
func CloseLog() {
	if logFile != nil {
		logFile.WriteString(fmt.Sprintf("\n--- SESSION END: %s ---\n", time.Now().Format(time.RFC3339)))
		logFile.Close()
		logFile = nil
	}
}

// GetLogWriter returns a Writer for output (screen + file)
func GetLogWriter() io.Writer {
	if logFile != nil {
		return io.MultiWriter(os.Stdout, logFile)
	}
	return os.Stdout
}

func LoadPassword(clusterName string) (string, error) {
	path, err := GetSecretPath(clusterName)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
