// @nexa $contract: "nexa.dev/source-comment/v1"
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// @nexa label.zh-CN: "审计记录"
// @nexa label.en-US: "Audit entry"
// @nexa description.zh-CN: "审计记录"
// @nexa description.en-US: "Audit entry"
// @nexa scope: "tenant"
type AuditEntry struct{ ent.Schema }

func (AuditEntry) Fields() []ent.Field {
	return []ent.Field{
		// @nexa label.zh-CN: "操作人"
		// @nexa label.en-US: "Actor"
		// @nexa description.zh-CN: "操作人"
		// @nexa description.en-US: "Operation actor"
		// @nexa ui.control: "member"
		// @nexa visibility: "public"
		field.String("actor"),
	}
}
