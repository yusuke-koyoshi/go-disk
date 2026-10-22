package disk

import (
	"io"

	"github.com/masahiro331/go-disk/fs"
	"github.com/masahiro331/go-disk/gpt"
	"github.com/masahiro331/go-disk/mbr"
	"github.com/masahiro331/go-disk/types"
	"golang.org/x/xerrors"
)

// NewDriver parses the partition table of r.
// The disk is GPT if any MBR entry has type 0xEE, and MBR otherwise. Linux also ignores MBR partitions in that case.
// r is read only with ReadAt, and partitions returned by the driver can be read concurrently
// if r supports concurrent ReadAt calls. *io.SectionReader satisfies io.ReaderAt.
//
// The logical block size is detected for GPT, as described in gpt.NewGUIDPartitionTableWithSectorSize,
// and is 512 bytes for MBR. An MBR disk with 4096-byte logical blocks needs NewDriverWithSectorSize,
// and so does a 4096-byte block disk that keeps a GPT header for 512-byte sectors at offset 512.
func NewDriver(r io.ReaderAt, checkFsFuncs ...fs.CheckFsFunc) (types.Driver, error) {
	return NewDriverWithSectorSize(r, 0, checkFsFuncs...)
}

// NewDriverWithSectorSize is NewDriver for a disk with the given logical block size, which must be 512 or 4096,
// or 0 to detect it as NewDriver does. Use it when the size is known, e.g. from the cloud disk the image was taken from.
func NewDriverWithSectorSize(r io.ReaderAt, sectorSize int, checkFsFuncs ...fs.CheckFsFunc) (types.Driver, error) {
	mbrSectorSize := sectorSize
	switch sectorSize {
	case 0:
		mbrSectorSize = mbr.Sector
	case 512, 4096:
	default:
		return nil, xerrors.Errorf("unsupported sector size: %d", sectorSize)
	}

	m, err := mbr.NewMasterBootRecordWithSectorSize(r, mbrSectorSize)
	if err != nil {
		if xerrors.Is(err, mbr.EmptyPartitionTable) || xerrors.Is(err, mbr.InvalidSignature) {
			ok, err := fs.CheckFileSystems(r, checkFsFuncs)
			if err != nil {
				return nil, xerrors.Errorf("failed to check filesystem: %w", err)
			}
			if ok {
				return fs.NewDirectFileSystem(r)
			}
		}
		return nil, xerrors.Errorf("failed to new MBR: %w", err)
	}

	if !hasGPTProtectiveEntry(m) {
		return m, nil
	}

	g, err := gpt.NewGUIDPartitionTableWithSectorSize(r, sectorSize)
	if err != nil {
		return nil, xerrors.Errorf("failed to parse GUID partition table: %w", err)
	}

	return g, nil
}

func hasGPTProtectiveEntry(m *mbr.MasterBootRecord) bool {
	for _, p := range m.Partitions {
		if p.Type == 0xEE {
			return true
		}
	}
	return false
}
