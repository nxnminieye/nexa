package svc

import "example.com/nexa-generation-consumer/backend/account/ent"

type ServiceContext struct {
	DB *ent.Client
}
