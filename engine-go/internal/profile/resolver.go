package profile

import (
	pprofile "github.com/fengzhizi319/PrivShield/pkg/profile"
)

// PrimitiveParams 隐私原语默认参数。
type PrimitiveParams = pprofile.PrimitiveParams

// PrivacyProfile 隐私参数配置。
type PrivacyProfile = pprofile.PrivacyProfile

// Resolver 隐私参数解析器。
type Resolver = pprofile.Resolver

// NewResolver 创建参数解析器。
func NewResolver() *Resolver {
	return pprofile.NewResolver()
}

// Validate 校验参数合法性。
func Validate(primitive string, params map[string]interface{}) error {
	return pprofile.Validate(primitive, params)
}
