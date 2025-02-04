package fs

import (
	"io"

	"github.com/masahiro331/go-disk/types"
)

type DirectFileSystem struct {
	Partition *DirectFileSystemPartition
}

func (d *DirectFileSystem) Next() (types.Partition, error) {
	if d.Partition == nil {
		return nil, io.EOF
	}
	partition := d.Partition
	d.Partition = nil

	return partition, nil
}

type DirectFileSystemPartition struct {
	sectionReader *io.SectionReader
}

func (p DirectFileSystemPartition) Index() int {
	return 0
}

func (p DirectFileSystemPartition) Name() string {
	return "0"
}

func (p DirectFileSystemPartition) GetType() []byte {
	return []byte{}
}

func (p DirectFileSystemPartition) GetStartSector() uint64 {
	return uint64(0)
}

func (p DirectFileSystemPartition) Bootable() bool {
	return false
}

func (p DirectFileSystemPartition) GetSize() uint64 {
	return uint64(p.sectionReader.Size())
}

func (p DirectFileSystemPartition) GetSectionReader() io.SectionReader {
	return *p.sectionReader
}

func (p DirectFileSystemPartition) IsSupported() bool {
	return true
}

func NewDirectFileSystem(sr *io.SectionReader) *DirectFileSystem {
	return &DirectFileSystem{
		Partition: &DirectFileSystemPartition{
			sectionReader: sr,
		},
	}
}
