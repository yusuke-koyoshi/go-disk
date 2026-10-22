package disk_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/masahiro331/go-disk"
	"github.com/masahiro331/go-disk/fs"
	"github.com/masahiro331/go-disk/gpt"
	"github.com/masahiro331/go-disk/mbr"
	"github.com/masahiro331/go-xfs-filesystem/xfs"
)

func TestNewDriver(t *testing.T) {
	tests := []struct {
		name         string
		inputFile    string
		wantErr      string
		checkFsFuncs []fs.CheckFsFunc
	}{
		{
			name:         "Happy path, Direct filesystem",
			inputFile:    "testdata/fs.bin",
			wantErr:      "",
			checkFsFuncs: []fs.CheckFsFunc{xfs.Check},
		},
		{
			name:         "Invalid path, Direct filesystem no check fs functions",
			inputFile:    "testdata/fs.bin",
			wantErr:      "Invalid master boot record signature",
			checkFsFuncs: []fs.CheckFsFunc{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := os.Open(tt.inputFile)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()

			info, err := f.Stat()
			if err != nil {
				t.Fatal(err)
			}
			sr := io.NewSectionReader(f, 0, info.Size())
			_, err = disk.NewDriver(sr, tt.checkFsFuncs...)

			if tt.wantErr == "" && err != nil {
				t.Errorf("input: %s, required no error = %v", tt.inputFile, err)
			} else if tt.wantErr != "" && !strings.HasSuffix(err.Error(), tt.wantErr) {
				t.Errorf("input: %s, expected: %q, actual: %q", tt.inputFile, tt.wantErr, err.Error())
			}
		})
	}
}

// newDisk builds a disk whose MBR has the given disk signature and entry types.
// A 0xEE entry covers the disk from LBA 1, as in a protective MBR, and other non-empty entries are at sector 40.
// If withGPT is true, a GPT header at LBA 1 has one used entry at sector 34.
func newDisk(signature uint32, entryTypes [4]byte, withGPT bool) []byte {
	return newDiskWithSectorSize(signature, entryTypes, withGPT, mbr.Sector)
}

// newDiskWithSectorSize is newDisk with sectorSize-byte logical blocks.
// The first byte of sectors 40 and 34, where the partitions start, is 1, to check where they are read from.
func newDiskWithSectorSize(signature uint32, entryTypes [4]byte, withGPT bool, sectorSize int) []byte {
	const sectors = 64
	buf := make([]byte, sectors*sectorSize)
	buf[40*sectorSize] = 1
	binary.LittleEndian.PutUint32(buf[440:], signature)
	for i, typ := range entryTypes {
		e := buf[446+16*i:]
		e[4] = typ
		switch typ {
		case 0:
		case 0xEE:
			binary.LittleEndian.PutUint32(e[8:], 1)
			binary.LittleEndian.PutUint32(e[12:], sectors-1)
		default:
			binary.LittleEndian.PutUint32(e[8:], 40)
			binary.LittleEndian.PutUint32(e[12:], 8)
		}
	}
	binary.LittleEndian.PutUint16(buf[510:], mbr.SIGNATURE)

	if withGPT {
		header := buf[sectorSize:]
		copy(header[0:8], gpt.Signature)
		binary.LittleEndian.PutUint32(header[12:], 92)  // HeaderSize
		binary.LittleEndian.PutUint64(header[24:], 1)   // MyLBA
		binary.LittleEndian.PutUint64(header[72:], 2)   // PartitionEntryLBA
		binary.LittleEndian.PutUint32(header[80:], 4)   // NumberOfPartitionEntries
		binary.LittleEndian.PutUint32(header[84:], 128) // SizeOfPartitionEntry
		buf[34*sectorSize] = 1
		entry := buf[2*sectorSize:]
		entry[0] = 0x01 // PartitionTypeGUID, non-zero to mark the entry as used
		binary.LittleEndian.PutUint64(entry[32:], 34)
		binary.LittleEndian.PutUint64(entry[40:], 39)
	}
	return buf
}

func TestNewDriver_PartitionTableType(t *testing.T) {
	tests := []struct {
		name       string
		signature  uint32
		entryTypes [4]byte
		withGPT    bool
		wantGPT    bool
		wantErr    string
	}{
		{name: "MBR with a zero signature", entryTypes: [4]byte{0x83}},
		{name: "MBR with a signature", signature: 0x12345678, entryTypes: [4]byte{0x83}},
		{name: "protective MBR with a zero signature", entryTypes: [4]byte{0xEE}, withGPT: true, wantGPT: true},
		{name: "protective MBR with a signature", signature: 0x12345678, entryTypes: [4]byte{0xEE}, withGPT: true, wantGPT: true},
		{name: "protective MBR without a GPT", signature: 0x12345678, entryTypes: [4]byte{0xEE}, wantErr: "invalid GPT signature"},
		{name: "hybrid MBR", entryTypes: [4]byte{0x83, 0, 0, 0xEE}, withGPT: true, wantGPT: true},
		// Like the Arch and Alpine ISOs, which Linux reads as MBR.
		{name: "GPT header without a protective entry", signature: 0x12345678, entryTypes: [4]byte{0, 0xEF}, withGPT: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := disk.NewDriver(bytes.NewReader(newDisk(tt.signature, tt.entryTypes, tt.withGPT)))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("NewDriver() = (%T, %v), want error containing %q", d, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewDriver() unexpected error: %v", err)
			}

			_, isGPT := d.(*gpt.GUIDPartitionTable)
			if isGPT != tt.wantGPT {
				t.Fatalf("NewDriver() = %T, want GPT: %v", d, tt.wantGPT)
			}
			wantStart := uint64(40)
			if tt.wantGPT {
				wantStart = 34
			}
			for {
				p, err := d.Next()
				if err != nil {
					t.Fatalf("Next() found no partition: %v", err)
				}
				if p.GetSize() == 0 {
					continue
				}
				if got := p.GetStartSector(); got != wantStart {
					t.Errorf("first partition start = %d, want %d", got, wantStart)
				}
				return
			}
		})
	}
}

func TestNewDriverWithSectorSize(t *testing.T) {
	gpt4K := newDiskWithSectorSize(0, [4]byte{0xEE}, true, 4096)
	mbr4K := newDiskWithSectorSize(0x12345678, [4]byte{0x83}, false, 4096)
	tests := []struct {
		name           string
		disk           []byte
		sectorSize     int
		wantGPT        bool
		wantSectorSize int
		wantErr        string
	}{
		{name: "detect a 4096 GPT", disk: gpt4K, sectorSize: 0, wantGPT: true, wantSectorSize: 4096},
		{name: "4096 GPT", disk: gpt4K, sectorSize: 4096, wantGPT: true, wantSectorSize: 4096},
		{name: "512 GPT", disk: newDisk(0, [4]byte{0xEE}, true), sectorSize: 512, wantGPT: true, wantSectorSize: 512},
		{name: "4096 MBR", disk: mbr4K, sectorSize: 4096, wantSectorSize: 4096},
		// The MBR does not record the logical block size, so it cannot be detected and 512 is used.
		{name: "4096 MBR without a sector size", disk: mbr4K, sectorSize: 0, wantSectorSize: 512},
		{name: "unsupported", disk: gpt4K, sectorSize: 1024, wantErr: "unsupported sector size"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := disk.NewDriverWithSectorSize(bytes.NewReader(tt.disk), tt.sectorSize)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("NewDriverWithSectorSize() = (%T, %v), want error containing %q", d, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewDriverWithSectorSize() unexpected error: %v", err)
			}

			p, err := d.Next()
			if err != nil {
				t.Fatalf("Next() unexpected error: %v", err)
			}
			var gotSectorSize int
			switch p := p.(type) {
			case *gpt.PartitionEntry:
				gotSectorSize = p.SectorSize()
			case *mbr.Partition:
				gotSectorSize = p.SectorSize()
			}
			if _, isGPT := d.(*gpt.GUIDPartitionTable); isGPT != tt.wantGPT || gotSectorSize != tt.wantSectorSize {
				t.Fatalf("NewDriverWithSectorSize() = %T with sector size %d, want GPT: %v, sector size %d", d, gotSectorSize, tt.wantGPT, tt.wantSectorSize)
			}
			b := make([]byte, 1)
			if _, err := p.ReadAt(b, 0); err != nil {
				t.Fatalf("ReadAt() unexpected error: %v", err)
			}
			if want := tt.disk[int(p.GetStartSector())*tt.wantSectorSize]; b[0] != want {
				t.Errorf("first byte of the partition = %d, want %d", b[0], want)
			}
		})
	}
}
