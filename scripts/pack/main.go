// pack 把发布文件打成 .tar.gz 或 .zip，并显式设置文件权限（程序 0755，其余 0644），
// 不依赖系统的 tar / zip，也不受构建平台文件系统权限的影响（例如在 Windows 上打 macOS / Linux 的包）。
//
// 用法：go run ./scripts/pack <输出文件> <包内目录名> <程序源路径>:<包内程序名> [其他文件…]
// 输出文件以 .zip 结尾时打 zip，否则打 tar.gz。
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type entry struct {
	src, name string
	mode      os.FileMode
}

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "用法：pack <输出文件> <包内目录名> <程序源路径>:<包内程序名> [其他文件…]")
		os.Exit(2)
	}
	out, dir := os.Args[1], os.Args[2]
	src, name, ok := strings.Cut(os.Args[3], ":")
	if !ok {
		fail(fmt.Errorf("程序参数应为 <源路径>:<包内程序名>，实际为 %q", os.Args[3]))
	}
	entries := []entry{{src: src, name: name, mode: 0o755}}
	for _, f := range os.Args[4:] {
		entries = append(entries, entry{src: f, name: filepath.Base(f), mode: 0o644})
	}
	var err error
	if strings.HasSuffix(out, ".zip") {
		err = writeZip(out, dir, entries)
	} else {
		err = writeTarGz(out, dir, entries)
	}
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "pack:", err)
	os.Exit(1)
}

// modTime 固定为源文件的修改时间，同样的输入得到同样的包
func modTime(src string) time.Time {
	if st, err := os.Stat(src); err == nil {
		return st.ModTime().UTC().Truncate(time.Second)
	}
	return time.Unix(0, 0)
}

func writeTarGz(out, dir string, entries []entry) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, _ := gzip.NewWriterLevel(f, gzip.BestCompression)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: dir + "/", Mode: 0o755, ModTime: modTime(entries[0].src)}); err != nil {
		return err
	}
	for _, e := range entries {
		data, err := os.ReadFile(e.src)
		if err != nil {
			return err
		}
		hdr := &tar.Header{Typeflag: tar.TypeReg, Name: path.Join(dir, e.name), Mode: int64(e.mode), Size: int64(len(data)), ModTime: modTime(e.src)}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return f.Close()
}

func writeZip(out, dir string, entries []entry) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for _, e := range entries {
		in, err := os.Open(e.src)
		if err != nil {
			return err
		}
		hdr := &zip.FileHeader{Name: path.Join(dir, e.name), Method: zip.Deflate, Modified: modTime(e.src)}
		hdr.SetMode(e.mode)
		w, err := zw.CreateHeader(hdr)
		if err == nil {
			_, err = io.Copy(w, in)
		}
		in.Close()
		if err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return f.Close()
}
