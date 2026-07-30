// @nexa $contract: "nexa.dev/source-comment/v1"
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// @nexa label.zh-CN: "账号"
// @nexa label.en-US: "Account"
// @nexa description.zh-CN: "业务账号"
// @nexa description.en-US: "Business account"
// @nexa scope: "tenant"
// @nexa crud.operations: ["list","get","create"]
type Account struct{ ent.Schema }

func (Account) Fields() []ent.Field {
	return []ent.Field{
		// @nexa label.zh-CN: "名称"
		// @nexa label.en-US: "Name"
		// @nexa description.zh-CN: "账号名称"
		// @nexa description.en-US: "Account name"
		// @nexa ui.control: "text"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "create-update"
		field.String("name"),
	}
}
