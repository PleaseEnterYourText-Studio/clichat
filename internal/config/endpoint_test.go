package config

import "testing"

func TestParseEndpoint(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		defPort int
		want    Endpoint
		wantErr bool
	}{
		{
			name: "只有主机名，用默认端口",
			raw:  "imap.example.com", defPort: DefaultIMAPPort,
			want: Endpoint{Host: "imap.example.com", Port: 993, TLS: TLSImplicit},
		},
		{
			name: "带端口",
			raw:  "imap.example.com:143", defPort: DefaultIMAPPort,
			want: Endpoint{Host: "imap.example.com", Port: 143, TLS: TLSImplicit},
		},
		{
			name: "首尾空格要去掉",
			raw:  "  smtp.example.com:587  ", defPort: DefaultSMTPPort,
			want: Endpoint{Host: "smtp.example.com", Port: 587, TLS: TLSImplicit},
		},
		{
			name: "IPv6 带方括号",
			raw:  "[::1]:993", defPort: DefaultIMAPPort,
			want: Endpoint{Host: "::1", Port: 993, TLS: TLSImplicit},
		},
		{name: "空输入", raw: "", defPort: DefaultIMAPPort, wantErr: true},
		{name: "只有空格", raw: "   ", defPort: DefaultIMAPPort, wantErr: true},
		{name: "端口不是数字", raw: "imap.example.com:abc", defPort: DefaultIMAPPort, wantErr: true},
		{name: "端口为 0", raw: "imap.example.com:0", defPort: DefaultIMAPPort, wantErr: true},
		{name: "端口越界", raw: "imap.example.com:70000", defPort: DefaultIMAPPort, wantErr: true},
		{name: "缺主机名", raw: ":993", defPort: DefaultIMAPPort, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEndpoint(tt.raw, tt.defPort)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseEndpoint(%q) 应该报错，却得到 %+v", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseEndpoint(%q) 意外报错: %v", tt.raw, err)
			}
			if got != tt.want {
				t.Errorf("ParseEndpoint(%q) = %+v, want %+v", tt.raw, got, tt.want)
			}
		})
	}
}

// 自定义预设要记下用户的选择，但不动服务器地址 —— 地址由向导手填。
func TestApplyProvider_RecordsCustomChoice(t *testing.T) {
	cfg := Default()
	ApplyProvider(cfg, FindProvider(CustomProviderID))

	if cfg.Account.Provider != CustomProviderID {
		t.Errorf("自定义选择没记下: %q", cfg.Account.Provider)
	}
	if cfg.Configured() {
		t.Error("刚选完自定义时配置不该是完整的 —— 地址还没填")
	}
}
