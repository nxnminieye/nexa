package protocol

import "github.com/nxnminieye/nexa/provenance"

func (d Document) Sources() []provenance.Source {
	if d.state == nil {
		return nil
	}
	return append([]provenance.Source(nil), d.state.sources...)
}
func (d Document) Source(ref provenance.SourceRef) (provenance.Source, bool) {
	if d.state == nil {
		return provenance.Source{}, false
	}
	index, ok := d.state.sourceIndex[ref.String()]
	if !ok {
		return provenance.Source{}, false
	}
	return d.state.sources[index], true
}
func (d Document) SourceDigest() provenance.Digest {
	if d.state == nil {
		return provenance.Digest{}
	}
	return d.state.sourceDigest
}
func (m Message) Source() provenance.Source {
	if m.state == nil {
		return provenance.Source{}
	}
	return m.state.source
}
func (m Message) CanonicalSourceJSON() []byte {
	if m.state == nil {
		return nil
	}
	return append([]byte(nil), m.state.canonicalSource...)
}
func (f Field) Source() provenance.Source {
	if f.state == nil {
		return provenance.Source{}
	}
	return f.state.source
}
func (f Field) CanonicalSourceJSON() []byte {
	if f.state == nil {
		return nil
	}
	return append([]byte(nil), f.state.canonicalSource...)
}
func (m Method) Source() provenance.Source {
	if m.state == nil {
		return provenance.Source{}
	}
	return m.state.source
}
func (m Method) CanonicalSourceJSON() []byte {
	if m.state == nil {
		return nil
	}
	return append([]byte(nil), m.state.canonicalSource...)
}
