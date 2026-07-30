package crudbuild

import "encoding/json"

type requiredIRCollections struct {
	Imports        *[]string            `json:"imports"`
	Enums          *[]requiredIREnum    `json:"enums"`
	Messages       *[]requiredIRMessage `json:"messages"`
	Services       *[]requiredIRService `json:"services"`
	TenantEntities *[]string            `json:"tenantEntities"`
	Sources        *[]wireSource        `json:"sources"`
}

type requiredIREnum struct {
	Values          *[]wireEnumValue `json:"values"`
	ReservedNames   *[]string        `json:"reservedNames"`
	ReservedNumbers *[]int32         `json:"reservedNumbers"`
}

type requiredIRMessage struct {
	Fields          *[]wireField `json:"fields"`
	ReservedNames   *[]string    `json:"reservedNames"`
	ReservedNumbers *[]int32     `json:"reservedNumbers"`
}

type requiredIRService struct {
	Methods *[]wireMethod `json:"methods"`
}

type requiredLockCollections struct {
	Schemas *[]requiredLockSchema `json:"schemas"`
}
type requiredLockSchema struct {
	Enums    *[]requiredLockEnum    `json:"enums"`
	Messages *[]requiredLockMessage `json:"messages"`
}
type requiredLockEnum struct {
	Current         *[]wireEnumAssignment `json:"current"`
	Retired         *[]wireEnumAssignment `json:"retired"`
	ReservedNames   *[]string             `json:"reservedNames"`
	ReservedNumbers *[]int32              `json:"reservedNumbers"`
}
type requiredLockMessage struct {
	Current         *[]wireAssignment `json:"current"`
	Retired         *[]wireAssignment `json:"retired"`
	ReservedNames   *[]string         `json:"reservedNames"`
	ReservedNumbers *[]int32          `json:"reservedNumbers"`
}

func validateIRCollectionPresence(data []byte) bool {
	var value requiredIRCollections
	if json.Unmarshal(data, &value) != nil || value.Imports == nil || value.Enums == nil || value.Messages == nil || value.Services == nil || value.TenantEntities == nil || value.Sources == nil {
		return false
	}
	for _, item := range *value.Enums {
		if item.Values == nil || item.ReservedNames == nil || item.ReservedNumbers == nil {
			return false
		}
	}
	for _, item := range *value.Messages {
		if item.Fields == nil || item.ReservedNames == nil || item.ReservedNumbers == nil {
			return false
		}
	}
	for _, item := range *value.Services {
		if item.Methods == nil {
			return false
		}
	}
	return true
}

func validateLockCollectionPresence(data []byte) bool {
	var value requiredLockCollections
	if json.Unmarshal(data, &value) != nil || value.Schemas == nil {
		return false
	}
	for _, schema := range *value.Schemas {
		if schema.Enums == nil || schema.Messages == nil {
			return false
		}
		for _, item := range *schema.Enums {
			if item.Current == nil || item.Retired == nil || item.ReservedNames == nil || item.ReservedNumbers == nil {
				return false
			}
		}
		for _, item := range *schema.Messages {
			if item.Current == nil || item.Retired == nil || item.ReservedNames == nil || item.ReservedNumbers == nil {
				return false
			}
		}
	}
	return true
}
