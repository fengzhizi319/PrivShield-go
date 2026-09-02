package grpcserver

import (
	"testing"

	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
)

func TestDatasourceMgrPermissionForGRPCMethod(t *testing.T) {
	cases := []struct {
		method string
		want   string
	}{
		{"/datasource.DataSourceManagerService/Health", ""},
		{"/datasource.DataSourceManagerService/GetData", "datasource:read"},
		{"/datasource.DataSourceManagerService/GetDataBySource", "datasource:read"},
		{"/datasource.DataSourceManagerService/GetDataSource", "datasource:read"},
		{"/datasource.DataSourceManagerService/ListDataSources", "datasource:read"},
		{"/datasource.DataSourceManagerService/GetYibaoData", "datasource:read"},
		{"/datasource.DataSourceManagerService/GetKangyangData", "datasource:read"},
		{"/datasource.DataSourceManagerService/GetMockData3", "datasource:read"},
		{"/datasource.DataSourceManagerService/GetMockData4", "datasource:read"},
		{"/datasource.DataSourceManagerService/ListMockSources", "datasource:read"},
		{"/datasource.DataSourceManagerService/TestConnection", "datasource:admin"},
		{"/unknown.Service/Foo", ""},
	}
	for _, tc := range cases {
		if got := DatasourceMgrPermissionForGRPCMethod(tc.method); got != tc.want {
			t.Errorf("DatasourceMgrPermissionForGRPCMethod(%q) = %q, want %q", tc.method, got, tc.want)
		}
	}
}

func TestIdentityScopeCheck(t *testing.T) {
	t.Run("wildcard grants all", func(t *testing.T) {
		id := &pkgauth.Identity{Scopes: []string{"*"}}
		if !id.HasPermission("datasource:read") {
			t.Error("wildcard should grant datasource:read")
		}
		if !id.HasPermission("datasource:admin") {
			t.Error("wildcard should grant datasource:admin")
		}
	})

	t.Run("scoped key grants only matching", func(t *testing.T) {
		id := &pkgauth.Identity{Scopes: []string{"datasource:read"}}
		if !id.HasPermission("datasource:read") {
			t.Error("datasource:read scope should grant datasource:read")
		}
		if id.HasPermission("datasource:admin") {
			t.Error("datasource:read scope should NOT grant datasource:admin")
		}
	})

	t.Run("empty scopes deny all", func(t *testing.T) {
		id := &pkgauth.Identity{Scopes: []string{}}
		if id.HasPermission("datasource:read") {
			t.Error("empty scopes should deny datasource:read")
		}
	})
}
