package security

import pkgauth "github.com/fengzhizi319/PrivShield/pkg/auth"

// Identity 表示已认证的调用者身份。
type Identity = pkgauth.Identity

// AnonymousIdentity 是认证未启用时使用的默认匿名管理员身份。
var AnonymousIdentity = pkgauth.AnonymousIdentity

// IdentityContextKey 用于在 gin.Context 中存储认证身份。
var IdentityContextKey = pkgauth.IdentityContextKey

// IsHealthPathOrMethod 判断给定 REST 路径或 gRPC 方法是否为健康探针。
func IsHealthPathOrMethod(pathOrMethod string) bool {
	return pkgauth.IsHealthPathOrMethod(pathOrMethod)
}

// PermissionForRESTPath 将 REST 路径映射为所需权限字符串。
func PermissionForRESTPath(path string) string {
	return pkgauth.PermissionForRESTPath(path)
}

// PermissionForGRPCMethod 将 gRPC 方法名映射为权限字符串。
func PermissionForGRPCMethod(method string) string {
	return pkgauth.PermissionForGRPCMethod(method)
}
