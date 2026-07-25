package ghctx

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// HashFiles は hashFiles() 相当のハッシュを計算する。
//
// GitHub と同じく、パターンに一致した各ファイルの SHA-256 を連結し、
// その全体の SHA-256 を返す。一致するファイルが無い場合は空文字列になる。
// パターン構文はワークフローのフィルタと同じものを使う。
func HashFiles(workDir string, patterns []string) (string, error) {
	if workDir == "" {
		workDir = "."
	}

	files, err := matchFiles(workDir, patterns)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		// 一致するファイルが無い場合、GitHub は空文字列を返す。
		return "", nil
	}

	// 走査順に依存しない結果にするため並べ替える。
	sort.Strings(files)

	outer := sha256.New()
	for _, f := range files {
		sum, err := hashFile(f)
		if err != nil {
			return "", err
		}
		outer.Write(sum)
	}
	return hex.EncodeToString(outer.Sum(nil)), nil
}

// matchFiles は作業ディレクトリを走査してパターンに一致するファイルを集める。
func matchFiles(workDir string, patterns []string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(workDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// リポジトリ管理用のディレクトリは対象外にする。
			switch d.Name() {
			case ".git", "node_modules":
				if path != workDir {
					return fs.SkipDir
				}
			}
			return nil
		}

		rel, err := filepath.Rel(workDir, path)
		if err != nil {
			return err
		}
		// パターンはスラッシュ区切りで書かれるため揃える。
		rel = filepath.ToSlash(rel)

		ok, err := matchGlobList(patterns, rel)
		if err != nil {
			return err
		}
		if ok {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%s の走査に失敗しました: %w", workDir, err)
	}
	return files, nil
}

// hashFile は1ファイルの SHA-256 を返す。
func hashFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%s を読めません: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, fmt.Errorf("%s の読み取りに失敗しました: %w", path, err)
	}
	return h.Sum(nil), nil
}
