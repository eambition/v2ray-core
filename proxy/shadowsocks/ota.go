package shadowsocks

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"io"

	"v2ray.com/core/common/alloc"
	"v2ray.com/core/common/log"
	"v2ray.com/core/common/serial"
	"v2ray.com/core/transport"
)

const (
	AuthSize = 10
)

type KeyGenerator func() []byte

type Authenticator struct {
	key KeyGenerator
}

func NewAuthenticator(keygen KeyGenerator) *Authenticator {
	return &Authenticator{
		key: keygen,
	}
}

func (this *Authenticator) Authenticate(auth []byte, data []byte) []byte {
	hasher := hmac.New(sha1.New, this.key())
	hasher.Write(data)
	res := hasher.Sum(nil)
	return append(auth, res[:AuthSize]...)
}

func HeaderKeyGenerator(key []byte, iv []byte) func() []byte {
	return func() []byte {
		newKey := make([]byte, 0, len(key)+len(iv))
		newKey = append(newKey, iv...)
		newKey = append(newKey, key...)
		return newKey
	}
}

func ChunkKeyGenerator(iv []byte) func() []byte {
	chunkId := 0
	return func() []byte {
		newKey := make([]byte, 0, len(iv)+4)
		newKey = append(newKey, iv...)
		newKey = serial.IntToBytes(chunkId, newKey)
		chunkId++
		return newKey
	}
}

type ChunkReader struct {
	reader io.Reader
	auth   *Authenticator
}

func NewChunkReader(reader io.Reader, auth *Authenticator) *ChunkReader {
	return &ChunkReader{
		reader: reader,
		auth:   auth,
	}
}

func (this *ChunkReader) Release() {
	this.reader = nil
	this.auth = nil
}

func (this *ChunkReader) Read() (*alloc.Buffer, error) {
	buffer := alloc.NewLargeBuffer()
	if _, err := io.ReadFull(this.reader, buffer.Value[:2]); err != nil {
		buffer.Release()
		return nil, err
	}
	// There is a potential buffer overflow here. Large buffer is 64K bytes,
	// while uin16 + 10 will be more than that
	length := serial.BytesToUint16(buffer.Value[:2]) + AuthSize
	if _, err := io.ReadFull(this.reader, buffer.Value[:length]); err != nil {
		buffer.Release()
		return nil, err
	}
	buffer.Slice(0, int(length))

	authBytes := buffer.Value[:AuthSize]
	payload := buffer.Value[AuthSize:]

	actualAuthBytes := this.auth.Authenticate(nil, payload)
	if !bytes.Equal(authBytes, actualAuthBytes) {
		buffer.Release()
		log.Debug("AuthenticationReader: Unexpected auth: ", authBytes)
		return nil, transport.ErrCorruptedPacket
	}
	buffer.SliceFrom(AuthSize)

	return buffer, nil
}

type ChunkWriter struct {
	writer io.Writer
	auth   *Authenticator
}

func NewChunkWriter(writer io.Writer, auth *Authenticator) *ChunkWriter {
	return &ChunkWriter{
		writer: writer,
		auth:   auth,
	}
}

func (this *ChunkWriter) Release() {
	this.writer = nil
	this.auth = nil
}

func (this *ChunkWriter) Write(payload *alloc.Buffer) (int, error) {
	totalLength := payload.Len()
	authBytes := this.auth.Authenticate(nil, payload.Bytes())
	payload.Prepend(authBytes)
	payload.PrependUint16(uint16(totalLength))
	return this.writer.Write(payload.Bytes())
}
