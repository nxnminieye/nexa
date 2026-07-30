// @nexa $contract: "nexa.dev/source-comment/v1"
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// @nexa label.zh-CN: "审计记录"
// @nexa label.en-US: "Audit entry"
// @nexa description.zh-CN: "操作审计记录"
// @nexa description.en-US: "Operation audit entry"
// @nexa scope: "tenant"
type AuditEntry struct{ ent.Schema }

func (AuditEntry) Fields() []ent.Field {
	return []ent.Field{
		// @nexa label.zh-CN: "操作人"
		// @nexa label.en-US: "Actor"
		// @nexa description.zh-CN: "执行操作的主体"
		// @nexa description.en-US: "Actor that performed the operation"
		// @nexa ui.control: "member"
		// @nexa visibility: "public"
		field.String("actor"),
		// @nexa label.zh-CN: "来源"
		// @nexa label.en-US: "Source"
		// @nexa description.zh-CN: "记录来源"
		// @nexa description.en-US: "Record source"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		field.String("source").Default("system").Immutable(),
	}
}
