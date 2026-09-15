package oatool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

func staticToken(string) func() (string, error) {
	return func() (string, error) { return "tok", nil }
}

// newServer 起一个记录调用次数的假 OA 服务端。
func newServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func toolNamed(t *testing.T, tools []tool.Tool, name string) tool.Tool {
	t.Helper()
	for _, tl := range tools {
		if tl.Name() == name {
			return tl
		}
	}
	t.Fatalf("tool %q not built; got %v", name, names(tools))
	return nil
}

func names(tools []tool.Tool) []string {
	out := make([]string, 0, len(tools))
	for _, tl := range tools {
		out = append(out, tl.Name())
	}
	return out
}

func runTool(t *testing.T, tl tool.Tool, args string) tool.Result {
	t.Helper()
	res, err := tl.Run(context.Background(), json.RawMessage(args), nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

func resultText(res tool.Result) string {
	var sb strings.Builder
	for _, c := range res.Content {
		sb.WriteString(c.Text)
	}
	return sb.String()
}

// TestGateBlocksBeforeAnyRequest 是本包最重要的一条:闸门关着时,**一个 OA 请求都
// 不能发出去**。整个"保密数据不出内网"的设计建立在"数据没被取出来"上,而不是
// "取出来了但没交给云端模型"——后者一旦进了工具结果就会进会话历史。
func TestGateBlocksBeforeAnyRequest(t *testing.T) {
	srv, calls := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"state":true,"data":{"待办数":3}}`))
	})
	tokenCalls := 0
	tools := New(Config{
		Endpoint: srv.URL + "/v1/oa",
		Token: func() (string, error) {
			tokenCalls++
			return "tok", nil
		},
		Gate: func(context.Context, string, string) error {
			return errors.New("当前会话不在本地模型上，已暂停 OA 访问。")
		},
	})
	res := runTool(t, toolNamed(t, tools, "oa_todo"), `{}`)

	if got := calls.Load(); got != 0 {
		t.Fatalf("gate closed but %d OA requests were sent; data must never leave OA", got)
	}
	if tokenCalls != 0 {
		t.Fatalf("gate closed but the token was fetched %d times; nothing may happen before the gate", tokenCalls)
	}
	if !res.IsError {
		t.Fatal("gate rejection must surface as an error result")
	}
	if !strings.Contains(resultText(res), "本地模型") {
		t.Fatalf("gate reason not surfaced to the model: %q", resultText(res))
	}
}

// TestGateOpenSendsRequest 是上一条的反证:没有闸门时请求照常发出,否则前一条测试
// 用一个永远不发请求的实现也能通过。
func TestGateOpenSendsRequest(t *testing.T) {
	var gotBody invokeRequest
	srv, calls := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q", got)
		}
		if !strings.HasSuffix(r.URL.Path, "/v1/oa/invoke") {
			t.Errorf("path = %q", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"state":true,"data":{"待办数":3,"列表":[]}}`))
	})
	tools := New(Config{Endpoint: srv.URL + "/v1/oa", Token: staticToken("tok")})
	res := runTool(t, toolNamed(t, tools, "oa_todo"), `{}`)

	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
	// 发出去的是**服务端的**工具名,不是带前缀的客户端名——前缀只为本地防撞名。
	if gotBody.Tool != "my_todo" {
		t.Fatalf("remote tool = %q, want my_todo", gotBody.Tool)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %q", resultText(res))
	}
	if got := resultText(res); got != `{"待办数":3,"列表":[]}` {
		t.Fatalf("data must pass through verbatim, got %q", got)
	}
}

// TestArgsForwardedAndFiltered 钉住两件事:声明过的参数原样转发,没声明的被丢掉。
// 入参会转给 OA,放任模型自造字段等于开一条没审视过的通路。
func TestArgsForwardedAndFiltered(t *testing.T) {
	var gotBody invokeRequest
	srv, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"state":true,"data":{"表单内容":{}}}`))
	})
	tools := New(Config{Endpoint: srv.URL + "/v1/oa", Token: staticToken("tok")})
	runTool(t, toolNamed(t, tools, "oa_request_content"), `{"requestId":" 12345 ","userid":"9","extra":1}`)

	if gotBody.Args["requestId"] != "12345" {
		t.Fatalf("requestId = %q, want trimmed 12345", gotBody.Args["requestId"])
	}
	// userid 尤其要挡:身份只能来自令牌,绝不能由模型指定。
	if _, ok := gotBody.Args["userid"]; ok {
		t.Fatal("undeclared arg userid was forwarded; identity must come from the token alone")
	}
	if _, ok := gotBody.Args["extra"]; ok {
		t.Fatal("undeclared arg extra was forwarded")
	}
}

// TestNumericIdAccepted 覆盖模型把流程号写成数字的常见情况。
func TestNumericIdAccepted(t *testing.T) {
	var gotBody invokeRequest
	srv, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"state":true,"data":{}}`))
	})
	tools := New(Config{Endpoint: srv.URL + "/v1/oa", Token: staticToken("tok")})
	runTool(t, toolNamed(t, tools, "oa_request_detail"), `{"requestId":1234567}`)

	if gotBody.Args["requestId"] != "1234567" {
		t.Fatalf("requestId = %q, want 1234567 (no scientific notation)", gotBody.Args["requestId"])
	}
}

// TestRequiredArgMissing:必填参数缺失是工具用错了,回 Go error 让引擎把它作为
// 工具失败上报,而不是白跑一趟 OA。
func TestRequiredArgMissing(t *testing.T) {
	srv, calls := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"state":true,"data":{}}`))
	})
	tools := New(Config{Endpoint: srv.URL + "/v1/oa", Token: staticToken("tok")})
	if _, err := toolNamed(t, tools, "oa_doc_content").Run(context.Background(), json.RawMessage(`{}`), nil, nil); err == nil {
		t.Fatal("missing required docId should fail")
	}
	if calls.Load() != 0 {
		t.Fatalf("a request was sent despite invalid input (%d calls)", calls.Load())
	}
}

// TestBusinessFailureCodes:三种业务失败的措辞各不相同——用户的应对完全不同,
// 塌成一句"查询失败"就等于让人瞎猜。
func TestBusinessFailureCodes(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"服务端给了说明就用它", `{"state":false,"code":"no_oa_access","message":"无权访问 OA 系统：该用户未绑定OA。"}`, "未绑定OA"},
		{"没给说明按码兜底", `{"state":false,"code":"identity_unavailable"}`, "稍后重试"},
		{"未知码也是一句人话", `{"state":false}`, "OA 查询失败"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte(tc.body))
			})
			tools := New(Config{Endpoint: srv.URL + "/v1/oa", Token: staticToken("tok")})
			res := runTool(t, toolNamed(t, tools, "oa_todo"), `{}`)
			if !res.IsError {
				t.Fatal("business failure must be an error result")
			}
			if !strings.Contains(resultText(res), tc.want) {
				t.Fatalf("message = %q, want it to contain %q", resultText(res), tc.want)
			}
		})
	}
}

// TestUnauthorizedRefreshesOnce 与联网搜索、LLM 请求同一套语义:401 时强制续期
// 一次再重试。桌面会话可以开很久,令牌过期是常态。
func TestUnauthorizedRefreshesOnce(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"message":"token expired"}`))
			return
		}
		w.Write([]byte(`{"state":true,"data":{"待办数":0}}`))
	}))
	t.Cleanup(srv.Close)

	refreshed := 0
	tools := New(Config{
		Endpoint:       srv.URL + "/v1/oa",
		Token:          staticToken("tok"),
		OnUnauthorized: func() { refreshed++ },
	})
	res := runTool(t, toolNamed(t, tools, "oa_todo"), `{}`)

	if refreshed != 1 {
		t.Fatalf("refreshed = %d, want 1", refreshed)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2 (original + one retry)", calls.Load())
	}
	if res.IsError {
		t.Fatalf("retry should have succeeded: %q", resultText(res))
	}
}

// TestHTTPErrorSurfacesUpstreamMessage:非 2xx 是"服务端拒绝",属于结果而不是
// 工具故障,模型看得懂也能换个说法再试。
func TestHTTPErrorSurfacesUpstreamMessage(t *testing.T) {
	srv, _ := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"message":"OA 不可达"}`))
	})
	tools := New(Config{Endpoint: srv.URL + "/v1/oa", Token: staticToken("tok")})
	res := runTool(t, toolNamed(t, tools, "oa_todo"), `{}`)
	if !res.IsError {
		t.Fatal("HTTP failure must be an error result")
	}
	text := resultText(res)
	if !strings.Contains(text, "502") || !strings.Contains(text, "OA 不可达") {
		t.Fatalf("upstream detail lost: %q", text)
	}
}

// TestNewRequiresEndpointAndToken:缺接线就一条都不装。装一批注定失败的工具比
// 没有更糟,而且白占上下文。
func TestNewRequiresEndpointAndToken(t *testing.T) {
	if got := New(Config{Token: staticToken("tok")}); got != nil {
		t.Fatalf("no endpoint should build nothing, got %v", names(got))
	}
	if got := New(Config{Endpoint: "https://bridge/v1/oa"}); got != nil {
		t.Fatalf("no token should build nothing, got %v", names(got))
	}
}

// TestCatalogShape 守住三条会在装配期或运行期咬人的约定。
func TestCatalogShape(t *testing.T) {
	seenName := map[string]bool{}
	seenRemote := map[string]bool{}
	for _, d := range catalog {
		// 会话内工具名唯一,撞名是装配失败而不是覆盖。
		if seenName[d.name] {
			t.Errorf("duplicate tool name %q", d.name)
		}
		seenName[d.name] = true
		if seenRemote[d.remote] {
			t.Errorf("duplicate remote name %q", d.remote)
		}
		seenRemote[d.remote] = true
		// 前缀是防撞名的唯一手段(见 catalog.go)。
		if !strings.HasPrefix(d.name, "oa_") {
			t.Errorf("tool %q must carry the oa_ prefix", d.name)
		}
		if strings.TrimSpace(d.desc) == "" {
			t.Errorf("tool %q has no description; that is the model's only clue", d.name)
		}
		// 身份绝不能出现在入参里。
		for _, a := range d.args {
			if strings.EqualFold(a.name, "userid") || strings.EqualFold(a.name, "userId") {
				t.Errorf("tool %q exposes an identity argument", d.name)
			}
		}
	}
	if len(Names()) != len(catalog) {
		t.Fatalf("Names() = %d entries, catalog has %d", len(Names()), len(catalog))
	}
}

// —— 扫描件公文：原图随结果带回，由会话模型自己读 ——

// 这是"保密数据只有一个出口"的落点：服务端不再自己调 VL 模型（那条是独立配置的，
// 配错就是一条没有报错的外泄通路），而是把原图交给已经锁定在内网模型上的会话。
func TestScannedDocImagesBecomeImageBlocks(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x01}
	body := `{"state":true,"data":{"扫描件":"共 2 页，请直接读图"},"images":[` +
		`{"mediaType":"image/jpeg","data":"` + base64.StdEncoding.EncodeToString(png) + `"},` +
		`{"mediaType":"image/png","data":"` + base64.StdEncoding.EncodeToString(png) + `"}]}`
	srv, _ := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(body))
	})
	tools := New(Config{Endpoint: srv.URL + "/v1/oa", Token: staticToken("tok")})
	res := runTool(t, toolNamed(t, tools, "oa_doc_content"), `{"docId":"123"}`)

	if res.IsError {
		t.Fatalf("unexpected error result: %q", resultText(res))
	}
	var text, images int
	for _, c := range res.Content {
		switch c.Type {
		case tool.ResultContentTypeText:
			text++
		case tool.ResultContentTypeImage:
			images++
			if c.Image == nil || len(c.Image.Data) == 0 {
				t.Error("image block carries no bytes")
			}
		}
	}
	if text != 1 {
		t.Errorf("text blocks = %d, want 1 (the JSON the model reads)", text)
	}
	if images != 2 {
		t.Errorf("image blocks = %d, want 2", images)
	}
	// 文字块里不能残留 base64——那会被当成正文喂给模型，既刷屏又白烧上下文。
	if strings.Contains(resultText(res), "iVBOR") || strings.Contains(resultText(res), "images") {
		t.Errorf("base64 leaked into the text block: %q", resultText(res))
	}
}

// 坏掉的条目静默跳过：一张图解不开不该让整次查询失败，文字部分通常已经够用。
func TestBrokenImagesAreSkippedNotFatal(t *testing.T) {
	good := base64.StdEncoding.EncodeToString([]byte{1, 2, 3})
	body := `{"state":true,"data":{"扫描件":"x"},"images":[` +
		`{"mediaType":"image/jpeg","data":"不是-base64!!"},` +
		`{"mediaType":"text/html","data":"` + good + `"},` +
		`{"mediaType":"image/jpeg","data":""},` +
		`{"mediaType":"image/jpeg","data":"` + good + `"}]}`
	srv, _ := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(body))
	})
	tools := New(Config{Endpoint: srv.URL + "/v1/oa", Token: staticToken("tok")})
	res := runTool(t, toolNamed(t, tools, "oa_doc_content"), `{"docId":"1"}`)

	if res.IsError {
		t.Fatalf("one bad image must not fail the whole query: %q", resultText(res))
	}
	images := 0
	for _, c := range res.Content {
		if c.Type == tool.ResultContentTypeImage {
			images++
		}
	}
	if images != 1 {
		t.Fatalf("image blocks = %d, want 1 (only the well-formed one)", images)
	}
}

// 客户端这一侧的张数上限：不信任上游。服务端某天把配置改大，不该让几十兆塞进
// 会话历史——而图片进了历史就再也压不掉。
func TestImageCountIsCappedClientSide(t *testing.T) {
	one := base64.StdEncoding.EncodeToString([]byte{1, 2, 3})
	items := make([]string, 0, maxImages+4)
	for i := 0; i < maxImages+4; i++ {
		items = append(items, `{"mediaType":"image/jpeg","data":"`+one+`"}`)
	}
	body := `{"state":true,"data":{},"images":[` + strings.Join(items, ",") + `]}`
	srv, _ := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(body))
	})
	tools := New(Config{Endpoint: srv.URL + "/v1/oa", Token: staticToken("tok")})
	res := runTool(t, toolNamed(t, tools, "oa_doc_content"), `{"docId":"1"}`)

	images := 0
	for _, c := range res.Content {
		if c.Type == tool.ResultContentTypeImage {
			images++
		}
	}
	if images != maxImages {
		t.Fatalf("image blocks = %d, want the cap %d", images, maxImages)
	}
}

// 没有图片的普通结果不受影响（绝大多数工具都走这条）。
func TestResultWithoutImagesIsTextOnly(t *testing.T) {
	srv, _ := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"state":true,"data":{"待办数":3}}`))
	})
	tools := New(Config{Endpoint: srv.URL + "/v1/oa", Token: staticToken("tok")})
	res := runTool(t, toolNamed(t, tools, "oa_todo"), `{}`)
	if len(res.Content) != 1 || res.Content[0].Type != tool.ResultContentTypeText {
		t.Fatalf("content = %+v, want a single text block", res.Content)
	}
}
