// @nexa $contract: "nexa.dev/source-comment/v1"
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// @nexa label.zh-CN: "账户"
// @nexa label.en-US: "Account"
// @nexa description.zh-CN: "账户记录"
// @nexa description.en-US: "Account record"
// @nexa scope: "tenant"
// @nexa crud.operations: ["list","get","create"]
type Account struct{ ent.Schema }

func (Account) Fields() []ent.Field {
	return []ent.Field{
		// @nexa label.zh-CN: "名称"
		// @nexa label.en-US: "Name"
		// @nexa description.zh-CN: "账户名称"
		// @nexa description.en-US: "Account name"
		// @nexa ui.control: "text"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "create-update"
		field.String("name"),
		// @nexa label.zh-CN: "状态"
		// @nexa label.en-US: "State"
		// @nexa description.zh-CN: "账户状态"
		// @nexa description.en-US: "Account state"
		// @nexa ui.control: "select"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "create-update"
		field.Enum("state").Values("active", "disabled").Default("active"),
		// @nexa label.zh-CN: "来源"
		// @nexa label.en-US: "Source"
		// @nexa description.zh-CN: "记录来源"
		// @nexa description.en-US: "Record source"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("source").Default("system").Immutable(),
	}
}
