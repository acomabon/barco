package data

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"time"

	"github.com/barcostreams/barco/internal/conf"
	"github.com/barcostreams/barco/internal/test/conf/mocks"
	tMocks "github.com/barcostreams/barco/internal/test/types/mocks"
	. "github.com/barcostreams/barco/internal/types"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
)

var _ = Describe("SegmentReader", func() {
	Describe("read()", func() {
		It("should support empty reads", func() {
			s := newTestReader()
			file, err := os.CreateTemp("", "segment_file*.dlog")
			Expect(err).NotTo(HaveOccurred())
			s.segmentFile = file

			go s.read()

			item := newTestReadItem()
			pollChunkAndAssert(s, item, 0)
		})

		It("should support partial chunks", func() {
			s := newTestReader()
			file, err := os.CreateTemp("", "segment_file*.dlog")
			Expect(err).NotTo(HaveOccurred())

			s.segmentFile, err = os.Open(file.Name())
			Expect(err).NotTo(HaveOccurred())
			chunkBuffer := createTestChunk(50, 10, 100)
			file.Write(chunkBuffer[:2])

			go s.read()

			item := newTestReadItem()
			// Initially is empty
			pollChunkAndAssert(s, item, 0)

			// Write the rest of the header: still empty result
			file.Write(chunkBuffer[2:chunkHeaderSize])
			pollChunkAndAssert(s, item, 0)

			// Write the rest of the body: data is returned
			file.Write(chunkBuffer[chunkHeaderSize:])
			pollChunkAndAssert(s, item, 50)

			close(s.Items)
		})

		It("should continue reading the next file", func() {
			config := new(mocks.Config)
			config.On("ReadAheadSize").Return(2048)
			config.On("AutoCommitInterval").Return(1 * time.Second)

			dir, err := os.MkdirTemp("", "read_next_file*")
			Expect(err).NotTo(HaveOccurred())
			firstFile, err := os.Create(filepath.Join(dir, "00000.dlog"))
			Expect(err).NotTo(HaveOccurred())
			secondFile, err := os.Create(filepath.Join(dir, "00020.dlog"))

			// Write to the files
			_, err = firstFile.Write(createTestChunk(512-chunkHeaderSize, 0, 20))
			Expect(err).NotTo(HaveOccurred())

			// buffer = createTestChunk(512-chunkHeaderSize, 50, 40)
			_, err = secondFile.Write(createTestChunk(512*2-chunkHeaderSize, 20, 30))
			Expect(err).NotTo(HaveOccurred())
			_, err = secondFile.Write(createTestChunk(512-chunkHeaderSize, 50, 40))
			Expect(err).NotTo(HaveOccurred())

			firstFile.Sync()
			secondFile.Sync()

			s := newTestReader()
			s.config = config
			s.basePath = dir

			go s.read()
			defer firstFile.Close()
			defer secondFile.Close()
			defer close(s.Items)

			item := newTestReadItem()
			pollChunkAndAssert(s, item, 512-chunkHeaderSize)
			// First empty poll
			pollChunkAndAssert(s, item, 0)
			// Second empty poll swaps the file
			pollChunkAndAssert(s, item, 0)
			pollChunkAndAssert(s, item, 512*2-chunkHeaderSize)
			pollChunkAndAssert(s, item, 512-chunkHeaderSize)
		})

		It("should read alignment", func() {
			config := new(mocks.Config)
			config.On("ReadAheadSize").Return(1024)
			config.On("AutoCommitInterval").Return(1 * time.Second)

			dir, err := os.MkdirTemp("", "read_alignment*")
			Expect(err).NotTo(HaveOccurred())
			file, err := os.Create(filepath.Join(dir, "00000.dlog"))
			Expect(err).NotTo(HaveOccurred())
			defer file.Close()

			// Write a chunk, followed by an alignment buffer
			_, err = file.Write(createTestChunk(510-chunkHeaderSize, 0, 20))
			Expect(err).NotTo(HaveOccurred())
			_, err = file.Write([]byte{0x80, 0x80})
			Expect(err).NotTo(HaveOccurred())
			_, err = file.Write(createTestChunk(512*2-3-chunkHeaderSize, 20, 15))
			Expect(err).NotTo(HaveOccurred())
			_, err = file.Write([]byte{0x80, 0x80, 0x80})

			file.Sync()

			s := newTestReader()
			s.config = config
			s.basePath = dir

			go s.read()
			defer close(s.Items)

			item := newTestReadItem()
			pollChunkAndAssert(s, item, 510-chunkHeaderSize)
			// First empty poll
			pollChunkAndAssert(s, item, 512*2-3-chunkHeaderSize)
			pollChunkAndAssert(s, item, 0)
		})

		It("should poll until there's new data", func() {
			config := new(mocks.Config)
			config.On("ReadAheadSize").Return(1024)
			config.On("AutoCommitInterval").Return(1 * time.Second)

			dir, err := os.MkdirTemp("", "poll_new_data*")
			Expect(err).NotTo(HaveOccurred())
			file, err := os.Create(filepath.Join(dir, "00000.dlog"))
			Expect(err).NotTo(HaveOccurred())
			defer file.Close()

			// Write a chunk, followed by an alignment buffer
			_, err = file.Write(createTestChunk(510-chunkHeaderSize, 0, 20))
			Expect(err).NotTo(HaveOccurred())
			_, err = file.Write([]byte{0x80, 0x80})
			Expect(err).NotTo(HaveOccurred())

			file.Sync()

			s := newTestReader()
			s.config = config
			s.basePath = dir

			go s.read()
			defer close(s.Items)

			item := newTestReadItem()
			pollChunkAndAssert(s, item, 510-chunkHeaderSize)
			pollChunkAndAssert(s, item, 0)
			pollChunkAndAssert(s, item, 0)
			pollChunkAndAssert(s, item, 0)

			// New data
			_, err = file.Write(createTestChunk(512-chunkHeaderSize, 20, 30))
			Expect(err).NotTo(HaveOccurred())
			file.Sync()

			pollChunkAndAssert(s, item, 512-chunkHeaderSize)
		})
	})
})

type testReadItem struct {
	chunkResult chan SegmentChunk
	errorResult chan error
}

func newTestReadItem() *testReadItem {
	return &testReadItem{
		chunkResult: make(chan SegmentChunk),
		errorResult: make(chan error),
	}
}

func (r *testReadItem) SetResult(err error, chunk SegmentChunk) {
	r.chunkResult <- chunk
	r.errorResult <- err
}

func createTestChunk(bodyLength, start, recordLength int) []byte {
	header := chunkHeader{
		Flags:        0,
		BodyLength:   uint32(bodyLength),
		Start:        int64(start),
		RecordLength: uint32(recordLength),
		Crc:          0,
	}

	buffer := new(bytes.Buffer)
	binary.Write(buffer, conf.Endianness, header.Flags)
	binary.Write(buffer, conf.Endianness, header.BodyLength)
	binary.Write(buffer, conf.Endianness, header.Start)
	binary.Write(buffer, conf.Endianness, header.RecordLength)
	header.Crc = crc32.ChecksumIEEE(buffer.Bytes())
	binary.Write(buffer, conf.Endianness, header.Crc)

	body := make([]byte, bodyLength)
	for i := 0; i < bodyLength; i++ {
		body[i] = byte(i)
	}
	buffer.Write(body)

	return buffer.Bytes()
}

func pollChunkAndAssert(s *SegmentReader, item *testReadItem, bodyLength int) {
	s.Items <- item
	chunk := <-item.chunkResult
	err := <-item.errorResult

	Expect(err).NotTo(HaveOccurred())
	Expect(chunk).ToNot(BeNil())
	Expect(chunk.DataBlock()).To(HaveLen(bodyLength))

	if bodyLength > 0 {
		expectedBody := make([]byte, bodyLength)
		for i := 0; i < bodyLength; i++ {
			expectedBody[i] = byte(i)
		}

		Expect(chunk.DataBlock()).To(Equal(expectedBody))
	}
}

func newTestReader() *SegmentReader {
	config := new(mocks.Config)
	config.On("ReadAheadSize").Return(1 * conf.Mib)
	config.On("AutoCommitInterval").Return(1 * time.Second)
	offsetState := new(tMocks.OffsetState)
	offsetState.On("Set",
		mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	return &SegmentReader{
		config:      config,
		Items:       make(chan ReadItem, 16),
		offsetState: offsetState,
		headerBuf:   make([]byte, chunkHeaderSize),
		isLeader:    true,
	}
}
