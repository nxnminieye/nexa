package lock

import (
	"math"
	"strconv"
	"unicode/utf8"

	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

const maxJCSSafeInteger int64 = 1<<53 - 1

func newSnapshot(ref release.Ref, profileID string, closure []string, target string, files []BaselineFile, source string, limits Limits, stage Stage) (Snapshot, *Error) {
	if pointer := firstInvalidTrackedSize(files); pointer != "" {
		return Snapshot{}, lockError(ErrLockInput, "source_lock_invalid", "tracked_file_size_invalid", pointer, stage)
	}
	payloadSize, ok := canonicalLockSize(ref, profileID, closure, target, files, limits.MaxDocumentBytes-1)
	if !ok || payloadSize < 0 || payloadSize >= int64(math.MaxInt) || payloadSize+1 > limits.MaxDocumentBytes {
		return Snapshot{}, lockError(ErrLockInput, "source_lock_invalid", "document_bytes_exceeded", "", stage)
	}
	key, err := NewKey(ref.ProviderID(), target)
	if err != nil {
		return Snapshot{}, lockError(ErrLockInput, "source_lock_invalid", "key_target_invalid", "/target", stage)
	}
	if source == "" {
		source = key.RepositoryPath()
	}
	encoder := canonicalEncoder{data: make([]byte, 0, int(payloadSize)+1), limit: payloadSize}
	encodeCanonicalLock(&encoder, ref, profileID, closure, target, files)
	if encoder.failed || encoder.size != payloadSize || int64(len(encoder.data)) != payloadSize {
		return Snapshot{}, lockError(ErrLockInternal, "source_lock_internal", "canonicalization_failed", "", stage)
	}
	digest := provenance.SHA256(encoder.data)
	encoder.data = append(encoder.data, '\n')
	return Snapshot{
		key: key, release: ref, profileID: profileID, profileClosure: append([]string(nil), closure...), target: target,
		trackedFiles: append([]BaselineFile(nil), files...), canonical: encoder.data, digest: digest, source: source, valid: true,
	}, nil
}

func canonicalLockSize(ref release.Ref, profileID string, closure []string, target string, files []BaselineFile, limit int64) (int64, bool) {
	if limit < 0 {
		return 0, false
	}
	encoder := canonicalEncoder{limit: limit, countOnly: true}
	encodeCanonicalLock(&encoder, ref, profileID, closure, target, files)
	return encoder.size, !encoder.failed
}

type canonicalEncoder struct {
	data      []byte
	size      int64
	limit     int64
	countOnly bool
	failed    bool
}

func (e *canonicalEncoder) literal(value string) {
	if e.failed || int64(len(value)) > e.limit-e.size {
		e.failed = true
		return
	}
	e.size += int64(len(value))
	if !e.countOnly {
		e.data = append(e.data, value...)
	}
}

func (e *canonicalEncoder) quoted(value string) {
	if !utf8.ValidString(value) {
		e.failed = true
		return
	}
	e.literal(`"`)
	for index := 0; index < len(value) && !e.failed; {
		character := value[index]
		if character >= utf8.RuneSelf {
			_, width := utf8.DecodeRuneInString(value[index:])
			e.literal(value[index : index+width])
			index += width
			continue
		}
		switch character {
		case '"', '\\':
			e.literal(`\` + string(character))
		case '\b':
			e.literal(`\b`)
		case '\t':
			e.literal(`\t`)
		case '\n':
			e.literal(`\n`)
		case '\f':
			e.literal(`\f`)
		case '\r':
			e.literal(`\r`)
		default:
			if character < 0x20 {
				const hex = "0123456789abcdef"
				e.literal(`\u00` + string([]byte{hex[character>>4], hex[character&0x0f]}))
			} else {
				e.literal(value[index : index+1])
			}
		}
		index++
	}
	e.literal(`"`)
}

func (e *canonicalEncoder) integer(value int64) {
	if value < 0 || value > maxJCSSafeInteger {
		e.failed = true
		return
	}
	var buffer [32]byte
	e.literal(string(strconv.AppendInt(buffer[:0], value, 10)))
}

func firstInvalidTrackedSize(files []BaselineFile) string {
	for index, file := range files {
		if file.size < 0 || file.size > maxJCSSafeInteger {
			return "/trackedFiles/" + strconv.Itoa(index) + "/size"
		}
	}
	return ""
}

func encodeCanonicalLock(e *canonicalEncoder, ref release.Ref, profileID string, closure []string, target string, files []BaselineFile) {
	e.literal(`{"apiVersion":`)
	e.quoted(APIVersion)
	e.literal(`,"kind":`)
	e.quoted(Kind)
	e.literal(`,"profileClosure":[`)
	for index, id := range closure {
		if index > 0 {
			e.literal(`,`)
		}
		e.quoted(id)
	}
	e.literal(`],"profileId":`)
	e.quoted(profileID)
	e.literal(`,"release":{"manifestDigest":`)
	e.quoted(ref.ManifestDigest().String())
	e.literal(`,"modulePath":`)
	e.quoted(ref.ModulePath())
	e.literal(`,"packagePath":`)
	e.quoted(ref.PackagePath())
	e.literal(`,"providerId":`)
	e.quoted(ref.ProviderID())
	e.literal(`,"treeDigest":`)
	e.quoted(ref.TreeDigest().String())
	e.literal(`,"version":`)
	e.quoted(ref.Version())
	e.literal(`},"target":`)
	e.quoted(target)
	e.literal(`,"trackedFiles":[`)
	for index, file := range files {
		if index > 0 {
			e.literal(`,`)
		}
		e.literal(`{"digest":`)
		e.quoted(file.Digest().String())
		e.literal(`,"mode":`)
		e.quoted(string(file.Mode()))
		e.literal(`,"path":`)
		e.quoted(file.Path())
		e.literal(`,"size":`)
		e.integer(file.Size())
		e.literal(`}`)
	}
	e.literal(`]}`)
}
