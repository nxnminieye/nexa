// @nexa $contract: "nexa.dev/source-comment/v1"
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// @nexa label.zh-CN: "任务租约"
// @nexa label.en-US: "Job lease"
// @nexa description.zh-CN: "任务租约"
// @nexa description.en-US: "Job lease"
// @nexa scope: "global"
type Lease struct{ ent.Schema }

func (Lease) Config() ent.Config { return ent.Config{Table: "job_leases"} }

func (Lease) Fields() []ent.Field {
	return []ent.Field{
		// @nexa label.zh-CN: "租约标识"
		// @nexa label.en-US: "Lease key"
		// @nexa description.zh-CN: "租约标识"
		// @nexa description.en-US: "Lease key"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "internal"
		field.String("key").Unique().Immutable(),
		// @nexa label.zh-CN: "运行标识"
		// @nexa label.en-US: "Run ID"
		// @nexa description.zh-CN: "运行标识"
		// @nexa description.en-US: "Run ID"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "internal"
		field.String("run_id").Immutable(),
		// @nexa label.zh-CN: "过期时间"
		// @nexa label.en-US: "Expires at"
		// @nexa description.zh-CN: "过期时间"
		// @nexa description.en-US: "Expires at"
		// @nexa ui.control: "datetime"
		// @nexa visibility: "internal"
		field.Time("expires_at"),
	}
}
