package main

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"

	"github.com/masahiro331/go-disk"
	"github.com/masahiro331/go-disk/gpt"
	"github.com/masahiro331/go-disk/types"
	"github.com/masahiro331/go-ext4-filesystem/ext4"
	"github.com/masahiro331/go-xfs-filesystem/xfs"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: ./main <disk image>")
	}
	f, err := os.Open(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		log.Fatal(err)
	}
	sr := io.NewSectionReader(f, 0, fi.Size())

	driver, err := disk.NewDriver(sr, ext4.Check, xfs.Check)
	if err != nil {
		log.Fatal(err)
	}

	for {
		partition, err := driver.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			log.Fatal(err)
		}

		if shouldSkip(partition) {
			log.Printf("skipping partition %s: not a target partition", partition.Name())
			continue
		}

		fmt.Printf("=== Partition: %s ===\n", partition.Name())

		psr := partition.GetSectionReader()

		fsys, err := newFS(psr)
		if err != nil {
			fmt.Printf("  skipping: %v\n", err)
			continue
		}

		err = fs.WalkDir(fsys, "/", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				log.Printf("walk error at %s: %v", path, err)
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			fmt.Printf("  %s\t%d\t%s\n", formatMode(info.Mode()), info.Size(), path)
			return nil
		})
		if err != nil {
			fmt.Printf("  walk error: %v\n", err)
		}
	}
}

type fsFactory struct {
	check func(io.Reader) bool
	newFS func(io.SectionReader) (fs.FS, error)
}

var fsFactories = []fsFactory{
	{check: ext4.Check, newFS: func(sr io.SectionReader) (fs.FS, error) { return ext4.NewFS(sr, nil) }},
	{check: xfs.Check, newFS: func(sr io.SectionReader) (fs.FS, error) { return xfs.NewFS(sr, nil) }},
}

var requiredDiskName = []string{
	"Linux",
	"p.lxroot",
	"primary",
	"ROOT",
	"0", "1", "2", "3",
}

func shouldSkip(partition types.Partition) bool {
	if bytes.Equal(partition.GetType(), []byte{0x00}) {
		return true
	}

	found := false
	for _, name := range requiredDiskName {
		if partition.Name() == name {
			found = true
			break
		}
	}
	if !found {
		return true
	}

	if p, ok := partition.(*gpt.PartitionEntry); ok {
		return p.Bootable()
	}
	return false
}

func formatMode(mode fs.FileMode) string {
	// ext4 の i_mode がそのまま fs.FileMode にキャストされているため、
	// 上位ビットから ext4 のファイルタイプを取得して ls -l 形式に変換する
	raw := uint16(mode)
	var typeChar byte
	switch raw & 0xF000 {
	case 0x4000:
		typeChar = 'd'
	case 0xA000:
		typeChar = 'l'
	case 0x2000:
		typeChar = 'c'
	case 0x6000:
		typeChar = 'b'
	case 0x1000:
		typeChar = 'p'
	case 0xC000:
		typeChar = 's'
	default:
		typeChar = '-'
	}

	perm := raw & 0x1FF
	buf := [10]byte{typeChar, '-', '-', '-', '-', '-', '-', '-', '-', '-'}
	const rwx = "rwx"
	for i := 0; i < 9; i++ {
		if perm&(1<<uint(8-i)) != 0 {
			buf[1+i] = rwx[i%3]
		}
	}
	return string(buf[:])
}

func newFS(sr io.SectionReader) (fs.FS, error) {
	for _, f := range fsFactories {
		if _, err := sr.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		if !f.check(&sr) {
			continue
		}
		if _, err := sr.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		return f.newFS(sr)
	}
	return nil, fmt.Errorf("unsupported filesystem")
}
