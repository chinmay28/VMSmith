package storage

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// Push sends an image to a remote host via SCP.
// target format: user@host (image goes to ~/.vmsmith/images/ on remote)
func (m *Manager) Push(imageName, target string) error {
	img, err := m.findImageByName(imageName)
	if err != nil {
		return err
	}

	remotePath := fmt.Sprintf("%s:.vmsmith/images/%s", target, filepath.Base(img.Path))
	cmd := exec.Command("scp", "-C", img.Path, remotePath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("scp push: %w", err)
	}

	return nil
}

// Pull downloads an image from a remote source.
// source can be:
//   - user@host/image-name  (SCP)
//   - http[s]://host/path   (HTTP)
func (m *Manager) Pull(source string) error {
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		return m.pullHTTP(source)
	}
	return m.pullSCP(source)
}

func (m *Manager) pullSCP(source string) error {
	// Parse user@host/image-name
	slashIdx := findLastSlash(source)
	if slashIdx < 0 {
		return fmt.Errorf("invalid SCP source %q: expected user@host/image-name", source)
	}
	host := source[:slashIdx]
	imageName := source[slashIdx+1:]

	localPath := filepath.Join(m.cfg.Storage.ImagesDir, imageName+".qcow2")
	remotePath := fmt.Sprintf("%s:.vmsmith/images/%s.qcow2", host, imageName)

	cmd := exec.Command("scp", "-C", remotePath, localPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("scp pull: %w", err)
	}

	return nil
}

func (m *Manager) pullHTTP(url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("http pull: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http pull: status %d", resp.StatusCode)
	}

	localPath := filepath.Join(m.cfg.Storage.ImagesDir, filepath.Base(url))
	f, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(localPath)
		return fmt.Errorf("downloading image: %w", err)
	}

	return nil
}

func (m *Manager) findImageByName(name string) (*types.Image, error) {
	imgs, err := m.store.ListImages()
	if err != nil {
		return nil, err
	}
	for _, img := range imgs {
		if img.Name == name {
			return img, nil
		}
	}
	return nil, fmt.Errorf("image %q not found", name)
}

func findLastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}
