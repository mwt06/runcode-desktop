# ouconline-ai-bridge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新建 Java 身份翻译代理 `ouconline-ai-bridge`：校验 Passport(IdentityServer4) 颁发的 JWT，把桌面端的 OpenAI 兼容请求翻译成 AI.Core 认可的 JSON Bearer 身份后转发（含 SSE 流式透传），并提供 `/api/me` 用户信息端点。

**Architecture:** 无状态单模块 Spring Boot 4 服务。`spring-security-oauth2-resource-server` 经 Passport 的 JWKS 自动验签；转发用 JDK 内置 `java.net.http.HttpClient`（虚拟线程 + 流拷贝逐块透传，不整体缓冲）；不落库、不做限流计费（AI.Core 已有）。

**Tech Stack:** JDK 21、Spring Boot 4.x（Maven，parent 继承）、spring-boot-starter-web / oauth2-resource-server / actuator、JUnit(starter-test) + spring-security-test + okhttp MockWebServer。

**Spec:** 上级设计文档 `docs/superpowers/specs/2026-07-14-passport-bridge-integration-design.md`（runcode_desktop 仓库）。

## Global Constraints

- 项目位置：`D:\ouconline\ouconline-ai-bridge`（**独立 git 仓库**，Task 1 里 `git init`；所有 commit 都在该仓库）。
- Maven 坐标：`groupId=cn.ouconline.ai`，`artifactId=ouconline-ai-bridge`，版本 `0.1.0-SNAPSHOT`；基础包 `cn.ouconline.ai.bridge`，子包 `auth` / `config` / `proxy` / `me`。
- JDK 21；Spring Boot parent `4.0.1`（若仓库已发布更高 4.0.x 补丁版可用之；不得降到 3.x）。
- 无数据库、无 Redis、无消息队列——本服务无状态。
- 只代理 `GET /v1/models`、`POST /v1/chat/completions` 与自有端点 `GET /api/me`、放行 `/actuator/health`。其余端点一律 401/404，不做。
- AI.Core 的 JSON Bearer 形如 `Bearer {"TenantId":"…","DependentType":"User","DependentId":"…","DependentName":"…(URL编码)"}`；.NET 侧解析是 `authorization.Replace("Bearer","").Trim()` 后 JSON 反序列化，所以 `Bearer ` 前缀 + 紧凑 JSON 即可。
- JWT claim 名区分大小写，与 Passport ApiScope UserClaims 一致：`TenantId`、`UserId`、`UserName`、`Name`、`Nickname`、`Avatar`。
- **前置假设**：桌面客户端申请的 scope 含带用户声明的 API scope（如 `passportapi`），否则 access token 无 `TenantId/UserId`，Bridge 按设计返回 403 `missing_claims`（这是子项目 B 的注册要求，不在本计划内解决）。
- AI.Core 的响应（含其 200+错误文本的现状）**原样透传**，Bridge 不二次包装。

### 版本适配说明（Boot 4 迁移期差异，遇到再改，不改任务结构）

- **Jackson**：Spring Boot 4 默认 Jackson 3（包名 `tools.jackson.*`，异常均为非受检的 `tools.jackson.core.JacksonException`）。本计划代码按 Jackson 3 书写。若实际解析出的 Boot 版本仍带 Jackson 2，把 import 换回 `com.fasterxml.jackson.*` 并按编译器提示补 `try/catch (JsonProcessingException)`。
- **`@LocalServerPort`**：优先 `org.springframework.boot.web.server.test.LocalServerPort`（Boot 4 模块化后位置）；编译不过就用 `org.springframework.boot.test.web.server.LocalServerPort`。
- **starter 名**：`spring-boot-starter-web` 在 Boot 4 仍可用；若解析失败改用 `spring-boot-starter-webmvc`。
- **Mock bean**：Boot 4 已移除 `@MockBean`，必须用 `org.springframework.test.context.bean.override.mockito.MockitoBean`。

## File Structure（最终形态）

```
D:\ouconline\ouconline-ai-bridge\
├── pom.xml
├── .gitignore
├── README.md                                   (Task 8)
├── Dockerfile                                  (Task 8)
├── yamls/bridge.yaml                           (Task 8, K8s Deployment+Service)
├── src/main/java/cn/ouconline/ai/bridge/
│   ├── BridgeApplication.java                  (Task 1)
│   ├── ApiExceptionHandler.java                (Task 4 建，Task 5/6 扩)
│   ├── auth/
│   │   ├── PassportClaims.java                 (Task 2)
│   │   └── MissingClaimException.java          (Task 2)
│   ├── config/
│   │   ├── SecurityConfig.java                 (Task 4)
│   │   └── BridgeProperties.java               (Task 5)
│   ├── proxy/
│   │   ├── AiCoreAuthorizationBuilder.java     (Task 3)
│   │   ├── UpstreamException.java              (Task 5)
│   │   ├── InvalidRequestBodyException.java    (Task 6)
│   │   ├── ProxyService.java                   (Task 5)
│   │   └── ProxyController.java                (Task 5 建 GET，Task 6 加 POST)
│   └── me/
│       ├── MeResponse.java                     (Task 4)
│       └── MeController.java                   (Task 4)
├── src/main/resources/application.yml          (Task 1)
└── src/test/java/cn/ouconline/ai/bridge/
    ├── BridgeApplicationTests.java             (Task 1)
    ├── auth/PassportClaimsTest.java            (Task 2)
    ├── proxy/AiCoreAuthorizationBuilderTest.java (Task 3)
    ├── me/MeControllerTest.java                (Task 4)
    ├── proxy/ModelsProxyTest.java              (Task 5)
    ├── proxy/UpstreamErrorTest.java            (Task 5)
    ├── proxy/ChatProxyTest.java                (Task 6)
    └── proxy/ChatStreamingProxyTest.java       (Task 7)
```

---

### Task 1: 项目脚手架与构建骨架

**Files:**
- Create: `D:\ouconline\ouconline-ai-bridge\pom.xml`
- Create: `D:\ouconline\ouconline-ai-bridge\.gitignore`
- Create: `D:\ouconline\ouconline-ai-bridge\src\main\java\cn\ouconline\ai\bridge\BridgeApplication.java`
- Create: `D:\ouconline\ouconline-ai-bridge\src\main\resources\application.yml`
- Test: `D:\ouconline\ouconline-ai-bridge\src\test\java\cn\ouconline\ai\bridge\BridgeApplicationTests.java`

**Interfaces:**
- Consumes: 无（首任务）
- Produces: 可构建、可测试的 Spring Boot 工程；配置键 `bridge.aicore.base-url`（Task 5 绑定）、`spring.security.oauth2.resourceserver.jwt.issuer-uri`（Task 4 的安全配置依赖其存在）

- [ ] **Step 1: 环境检查**

Run: `java -version` → 期望 `openjdk version "21.x"`；`mvn -version` → 期望 Apache Maven 3.9+。
缺 JDK：`winget install EclipseAdoptium.Temurin.21.JDK`；缺 Maven：`winget install Apache.Maven`（装完重开终端）。

- [ ] **Step 2: 创建目录与 git 仓库**

```powershell
New-Item -ItemType Directory -Force D:\ouconline\ouconline-ai-bridge
Set-Location D:\ouconline\ouconline-ai-bridge
git init
```

- [ ] **Step 3: 写 pom.xml**

```xml
<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0"
         xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
         xsi:schemaLocation="http://maven.apache.org/POM/4.0.0 https://maven.apache.org/xsd/maven-4.0.0.xsd">
  <modelVersion>4.0.0</modelVersion>

  <parent>
    <groupId>org.springframework.boot</groupId>
    <artifactId>spring-boot-starter-parent</artifactId>
    <version>4.0.1</version>
    <relativePath/>
  </parent>

  <groupId>cn.ouconline.ai</groupId>
  <artifactId>ouconline-ai-bridge</artifactId>
  <version>0.1.0-SNAPSHOT</version>
  <name>ouconline-ai-bridge</name>
  <description>Passport JWT 到 AI.Core 租户身份的翻译代理</description>

  <properties>
    <java.version>21</java.version>
  </properties>

  <dependencies>
    <dependency>
      <groupId>org.springframework.boot</groupId>
      <artifactId>spring-boot-starter-web</artifactId>
    </dependency>
    <dependency>
      <groupId>org.springframework.boot</groupId>
      <artifactId>spring-boot-starter-oauth2-resource-server</artifactId>
    </dependency>
    <dependency>
      <groupId>org.springframework.boot</groupId>
      <artifactId>spring-boot-starter-actuator</artifactId>
    </dependency>

    <dependency>
      <groupId>org.springframework.boot</groupId>
      <artifactId>spring-boot-starter-test</artifactId>
      <scope>test</scope>
    </dependency>
    <dependency>
      <groupId>org.springframework.security</groupId>
      <artifactId>spring-security-test</artifactId>
      <scope>test</scope>
    </dependency>
    <dependency>
      <groupId>com.squareup.okhttp3</groupId>
      <artifactId>mockwebserver</artifactId>
      <version>4.12.0</version>
      <scope>test</scope>
    </dependency>
  </dependencies>

  <build>
    <plugins>
      <plugin>
        <groupId>org.springframework.boot</groupId>
        <artifactId>spring-boot-maven-plugin</artifactId>
      </plugin>
    </plugins>
  </build>
</project>
```

- [ ] **Step 4: 写 .gitignore**

```gitignore
target/
.idea/
*.iml
.vscode/
.mvn/wrapper/maven-wrapper.jar
*.log
```

- [ ] **Step 5: 写主类与配置**

`src/main/java/cn/ouconline/ai/bridge/BridgeApplication.java`:

```java
package cn.ouconline.ai.bridge;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.boot.context.properties.ConfigurationPropertiesScan;

@SpringBootApplication
@ConfigurationPropertiesScan
public class BridgeApplication {
    public static void main(String[] args) {
        SpringApplication.run(BridgeApplication.class, args);
    }
}
```

`src/main/resources/application.yml`:

```yaml
server:
  port: 8080

spring:
  application:
    name: ouconline-ai-bridge
  threads:
    virtual:
      enabled: true
  security:
    oauth2:
      resourceserver:
        jwt:
          # Passport(IdentityServer4) 的 issuer；生产经环境变量覆盖，
          # 如 https://passport.le.ouc-online.com.cn/
          issuer-uri: ${BRIDGE_PASSPORT_ISSUER_URI:http://localhost:5000}

bridge:
  aicore:
    # AI.Core 的根地址（不含 /v1），生产经环境变量覆盖
    base-url: ${BRIDGE_AICORE_BASE_URL:http://localhost:5100}
    connect-timeout: 10s
    # 上游首包超时（JDK HttpClient 的 request timeout 只覆盖到响应头到达，
    # 不限制流式 body 的读取时长，因此对 SSE 长响应安全）
    response-timeout: 5m

management:
  endpoints:
    web:
      exposure:
        include: health
```

- [ ] **Step 6: 写冒烟测试**

`src/test/java/cn/ouconline/ai/bridge/BridgeApplicationTests.java`:

```java
package cn.ouconline.ai.bridge;

import org.junit.jupiter.api.Test;
import org.springframework.boot.test.context.SpringBootTest;

@SpringBootTest
class BridgeApplicationTests {

    @Test
    void contextLoads() {
        // issuer-uri 指向的 Passport 不需要在线：
        // Boot 用 SupplierJwtDecoder 惰性解析，首个令牌到来才访问 JWKS
    }
}
```

- [ ] **Step 7: 构建并跑冒烟测试**

Run: `mvn test`（在 `D:\ouconline\ouconline-ai-bridge` 下）
Expected: `BUILD SUCCESS`，`Tests run: 1, Failures: 0`。首次运行会下载依赖，耗时数分钟。
若 parent 4.0.1 解析失败：改用 `mvn -U test` 刷新；仍失败则把 parent 版本换成仓库里最新的 4.0.x。

- [ ] **Step 8: Commit**

```powershell
git add pom.xml .gitignore src
git commit -m "chore: Spring Boot 4 项目脚手架（web/oauth2-resource-server/actuator）"
```

---

### Task 2: PassportClaims —— 从 JWT 提取用户声明

**Files:**
- Create: `src\main\java\cn\ouconline\ai\bridge\auth\PassportClaims.java`
- Create: `src\main\java\cn\ouconline\ai\bridge\auth\MissingClaimException.java`
- Test: `src\test\java\cn\ouconline\ai\bridge\auth\PassportClaimsTest.java`

**Interfaces:**
- Consumes: `org.springframework.security.oauth2.jwt.Jwt`（Spring Security 类型）
- Produces: `record PassportClaims(String tenantId, String userId, String userName, String name, String nickname, String avatar)`，静态工厂 `PassportClaims.from(Jwt)`（TenantId/UserId 缺失时抛 `MissingClaimException extends RuntimeException`）。Task 3/4/5/6 都消费此类型。

- [ ] **Step 1: 写失败测试**

`src/test/java/cn/ouconline/ai/bridge/auth/PassportClaimsTest.java`:

```java
package cn.ouconline.ai.bridge.auth;

import org.junit.jupiter.api.Test;
import org.springframework.security.oauth2.jwt.Jwt;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

class PassportClaimsTest {

    private Jwt.Builder baseJwt() {
        return Jwt.withTokenValue("token").header("alg", "RS256").subject("zhangsan");
    }

    @Test
    void fromExtractsAllClaims() {
        Jwt jwt = baseJwt()
            .claim("TenantId", "t-1").claim("UserId", "u-1")
            .claim("UserName", "zhangsan").claim("Name", "张三")
            .claim("Nickname", "小张").claim("Avatar", "http://a/x.png")
            .build();
        PassportClaims c = PassportClaims.from(jwt);
        assertThat(c.tenantId()).isEqualTo("t-1");
        assertThat(c.userId()).isEqualTo("u-1");
        assertThat(c.userName()).isEqualTo("zhangsan");
        assertThat(c.name()).isEqualTo("张三");
        assertThat(c.nickname()).isEqualTo("小张");
        assertThat(c.avatar()).isEqualTo("http://a/x.png");
    }

    @Test
    void fromFailsWithoutTenantId() {
        Jwt jwt = baseJwt().claim("UserId", "u-1").build();
        assertThatThrownBy(() -> PassportClaims.from(jwt))
            .isInstanceOf(MissingClaimException.class);
    }

    @Test
    void fromFailsWithoutUserId() {
        Jwt jwt = baseJwt().claim("TenantId", "t-1").build();
        assertThatThrownBy(() -> PassportClaims.from(jwt))
            .isInstanceOf(MissingClaimException.class);
    }

    @Test
    void optionalClaimsMayBeAbsent() {
        Jwt jwt = baseJwt().claim("TenantId", "t-1").claim("UserId", "u-1").build();
        PassportClaims c = PassportClaims.from(jwt);
        assertThat(c.name()).isNull();
        assertThat(c.nickname()).isNull();
        assertThat(c.avatar()).isNull();
    }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `mvn test -Dtest=PassportClaimsTest`
Expected: 编译错误 `cannot find symbol: class PassportClaims`。

- [ ] **Step 3: 最小实现**

`src/main/java/cn/ouconline/ai/bridge/auth/MissingClaimException.java`:

```java
package cn.ouconline.ai.bridge.auth;

/** 访问令牌缺少必需的用户声明（通常是客户端 scope 配置问题）。 */
public class MissingClaimException extends RuntimeException {
    public MissingClaimException(String message) {
        super(message);
    }
}
```

`src/main/java/cn/ouconline/ai/bridge/auth/PassportClaims.java`:

```java
package cn.ouconline.ai.bridge.auth;

import org.springframework.security.oauth2.jwt.Jwt;

/**
 * Passport(IdentityServer4) 访问令牌中本服务关心的声明。
 * 声明名与 Passport ApiScope 的 UserClaims 定义一致，区分大小写。
 */
public record PassportClaims(String tenantId, String userId, String userName,
                             String name, String nickname, String avatar) {

    public static final String CLAIM_TENANT_ID = "TenantId";
    public static final String CLAIM_USER_ID = "UserId";
    public static final String CLAIM_USER_NAME = "UserName";
    public static final String CLAIM_NAME = "Name";
    public static final String CLAIM_NICKNAME = "Nickname";
    public static final String CLAIM_AVATAR = "Avatar";

    public static PassportClaims from(Jwt jwt) {
        String tenantId = jwt.getClaimAsString(CLAIM_TENANT_ID);
        String userId = jwt.getClaimAsString(CLAIM_USER_ID);
        if (isBlank(tenantId) || isBlank(userId)) {
            throw new MissingClaimException(
                "令牌缺少 TenantId/UserId 声明，请检查客户端申请的 scope 是否包含带用户声明的 API scope（如 passportapi）");
        }
        return new PassportClaims(tenantId, userId,
            jwt.getClaimAsString(CLAIM_USER_NAME),
            jwt.getClaimAsString(CLAIM_NAME),
            jwt.getClaimAsString(CLAIM_NICKNAME),
            jwt.getClaimAsString(CLAIM_AVATAR));
    }

    private static boolean isBlank(String s) {
        return s == null || s.isBlank();
    }
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `mvn test -Dtest=PassportClaimsTest`
Expected: `Tests run: 4, Failures: 0`。

- [ ] **Step 5: Commit**

```powershell
git add src
git commit -m "feat: PassportClaims 从访问令牌提取用户声明"
```

---

### Task 3: AiCoreAuthorizationBuilder —— 构造 AI.Core 的 JSON Bearer

**Files:**
- Create: `src\main\java\cn\ouconline\ai\bridge\proxy\AiCoreAuthorizationBuilder.java`
- Test: `src\test\java\cn\ouconline\ai\bridge\proxy\AiCoreAuthorizationBuilderTest.java`

**Interfaces:**
- Consumes: `PassportClaims`（Task 2）
- Produces: `AiCoreAuthorizationBuilder.build(PassportClaims): String` —— 返回完整头值 `"Bearer {...json...}"`。Task 5 的 `ProxyService` 消费。

- [ ] **Step 1: 写失败测试**

`src/test/java/cn/ouconline/ai/bridge/proxy/AiCoreAuthorizationBuilderTest.java`:

```java
package cn.ouconline.ai.bridge.proxy;

import cn.ouconline.ai.bridge.auth.PassportClaims;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.ObjectMapper;

import static org.assertj.core.api.Assertions.assertThat;

class AiCoreAuthorizationBuilderTest {

    private final ObjectMapper mapper = new ObjectMapper();

    private JsonNode parse(String header) {
        assertThat(header).startsWith("Bearer ");
        return mapper.readTree(header.substring("Bearer ".length()));
    }

    @Test
    void buildsUserJsonBearer() {
        var claims = new PassportClaims("t-1", "u-1", "zhangsan", "张三", null, null);
        JsonNode node = parse(AiCoreAuthorizationBuilder.build(claims));
        assertThat(node.get("TenantId").asText()).isEqualTo("t-1");
        assertThat(node.get("DependentType").asText()).isEqualTo("User");
        assertThat(node.get("DependentId").asText()).isEqualTo("u-1");
        // .NET 侧 WebUtility.UrlDecode 还原中文
        assertThat(node.get("DependentName").asText()).isEqualTo("%E5%BC%A0%E4%B8%89");
    }

    @Test
    void fallsBackToUserNameWhenNameMissing() {
        var claims = new PassportClaims("t-1", "u-1", "zhangsan", null, null, null);
        JsonNode node = parse(AiCoreAuthorizationBuilder.build(claims));
        assertThat(node.get("DependentName").asText()).isEqualTo("zhangsan");
    }

    @Test
    void omitsDependentNameWhenNoNameAtAll() {
        var claims = new PassportClaims("t-1", "u-1", null, null, null, null);
        JsonNode node = parse(AiCoreAuthorizationBuilder.build(claims));
        assertThat(node.has("DependentName")).isFalse();
    }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `mvn test -Dtest=AiCoreAuthorizationBuilderTest`
Expected: 编译错误 `cannot find symbol: class AiCoreAuthorizationBuilder`。
（若 `tools.jackson` 包不存在，说明该 Boot 版本仍是 Jackson 2——按"版本适配说明"把本任务与 Task 5/6 的 import 换成 `com.fasterxml.jackson.databind.*`。）

- [ ] **Step 3: 最小实现**

`src/main/java/cn/ouconline/ai/bridge/proxy/AiCoreAuthorizationBuilder.java`:

```java
package cn.ouconline.ai.bridge.proxy;

import cn.ouconline.ai.bridge.auth.PassportClaims;
import tools.jackson.databind.ObjectMapper;
import tools.jackson.databind.node.ObjectNode;

import java.net.URLEncoder;
import java.nio.charset.StandardCharsets;

/**
 * 构造 AI.Core 认可的 JSON Bearer（信任模式：AI.Core 假定上游已完成主鉴权）。
 * 形如 Bearer {"TenantId":"…","DependentType":"User","DependentId":"…","DependentName":"…"}
 * DependentName 需 URL 编码，.NET 侧用 WebUtility.UrlDecode 还原。
 */
public final class AiCoreAuthorizationBuilder {

    public static final String DEPENDENT_TYPE_USER = "User";
    private static final ObjectMapper MAPPER = new ObjectMapper();

    private AiCoreAuthorizationBuilder() {
    }

    public static String build(PassportClaims claims) {
        ObjectNode node = MAPPER.createObjectNode();
        node.put("TenantId", claims.tenantId());
        node.put("DependentType", DEPENDENT_TYPE_USER);
        node.put("DependentId", claims.userId());
        String displayName = firstNonBlank(claims.name(), claims.userName());
        if (displayName != null) {
            node.put("DependentName", URLEncoder.encode(displayName, StandardCharsets.UTF_8));
        }
        return "Bearer " + MAPPER.writeValueAsString(node);
    }

    private static String firstNonBlank(String a, String b) {
        if (a != null && !a.isBlank()) return a;
        if (b != null && !b.isBlank()) return b;
        return null;
    }
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `mvn test -Dtest=AiCoreAuthorizationBuilderTest`
Expected: `Tests run: 3, Failures: 0`。

- [ ] **Step 5: Commit**

```powershell
git add src
git commit -m "feat: AiCoreAuthorizationBuilder 构造 AI.Core JSON Bearer"
```

---

### Task 4: SecurityConfig + 异常映射 + /api/me

**Files:**
- Create: `src\main\java\cn\ouconline\ai\bridge\config\SecurityConfig.java`
- Create: `src\main\java\cn\ouconline\ai\bridge\ApiExceptionHandler.java`
- Create: `src\main\java\cn\ouconline\ai\bridge\me\MeResponse.java`
- Create: `src\main\java\cn\ouconline\ai\bridge\me\MeController.java`
- Test: `src\test\java\cn\ouconline\ai\bridge\me\MeControllerTest.java`

**Interfaces:**
- Consumes: `PassportClaims.from(Jwt)`、`MissingClaimException`（Task 2）
- Produces: 全局安全规则（`/actuator/health/**` 放行、其余需认证、无状态、CSRF 关）；`GET /api/me` → `MeResponse(userId, userName, name, nickname, avatar, tenantId)`；`ApiExceptionHandler`（Task 5/6 会往里加 handler）。

- [ ] **Step 1: 写失败测试**

`src/test/java/cn/ouconline/ai/bridge/me/MeControllerTest.java`:

```java
package cn.ouconline.ai.bridge.me;

import cn.ouconline.ai.bridge.config.SecurityConfig;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.WebMvcTest;
import org.springframework.context.annotation.Import;
import org.springframework.security.oauth2.jwt.JwtDecoder;
import org.springframework.test.context.bean.override.mockito.MockitoBean;
import org.springframework.test.web.servlet.MockMvc;

import static org.springframework.security.test.web.servlet.request.SecurityMockMvcRequestPostProcessors.jwt;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

@WebMvcTest(MeController.class)
@Import(SecurityConfig.class)
class MeControllerTest {

    @Autowired
    MockMvc mockMvc;

    @MockitoBean
    JwtDecoder jwtDecoder;

    @Test
    void requiresAuthentication() throws Exception {
        mockMvc.perform(get("/api/me")).andExpect(status().isUnauthorized());
    }

    @Test
    void returnsUserInfoFromClaims() throws Exception {
        mockMvc.perform(get("/api/me").with(jwt().jwt(j -> j
                .claim("TenantId", "t-1").claim("UserId", "u-1")
                .claim("UserName", "zhangsan").claim("Name", "张三")
                .claim("Nickname", "小张").claim("Avatar", "http://a/x.png"))))
            .andExpect(status().isOk())
            .andExpect(jsonPath("$.userId").value("u-1"))
            .andExpect(jsonPath("$.userName").value("zhangsan"))
            .andExpect(jsonPath("$.name").value("张三"))
            .andExpect(jsonPath("$.nickname").value("小张"))
            .andExpect(jsonPath("$.avatar").value("http://a/x.png"))
            .andExpect(jsonPath("$.tenantId").value("t-1"));
    }

    @Test
    void missingClaimsYields403() throws Exception {
        mockMvc.perform(get("/api/me").with(jwt())) // jwt() 默认无自定义 claims
            .andExpect(status().isForbidden())
            .andExpect(jsonPath("$.error").value("missing_claims"));
    }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `mvn test -Dtest=MeControllerTest`
Expected: 编译错误（`MeController`、`SecurityConfig` 不存在）。

- [ ] **Step 3: 最小实现**

`src/main/java/cn/ouconline/ai/bridge/config/SecurityConfig.java`:

```java
package cn.ouconline.ai.bridge.config;

import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.security.config.Customizer;
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.config.annotation.web.configuration.EnableWebSecurity;
import org.springframework.security.config.http.SessionCreationPolicy;
import org.springframework.security.web.SecurityFilterChain;

@Configuration
@EnableWebSecurity
public class SecurityConfig {

    @Bean
    SecurityFilterChain securityFilterChain(HttpSecurity http) throws Exception {
        http
            .csrf(csrf -> csrf.disable())
            .sessionManagement(sm -> sm.sessionCreationPolicy(SessionCreationPolicy.STATELESS))
            .authorizeHttpRequests(auth -> auth
                .requestMatchers("/actuator/health/**").permitAll()
                .anyRequest().authenticated())
            .oauth2ResourceServer(oauth2 -> oauth2.jwt(Customizer.withDefaults()));
        return http.build();
    }
}
```

`src/main/java/cn/ouconline/ai/bridge/ApiExceptionHandler.java`:

```java
package cn.ouconline.ai.bridge;

import cn.ouconline.ai.bridge.auth.MissingClaimException;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.ExceptionHandler;
import org.springframework.web.bind.annotation.RestControllerAdvice;

import java.util.Map;

@RestControllerAdvice
public class ApiExceptionHandler {

    @ExceptionHandler(MissingClaimException.class)
    ResponseEntity<Map<String, String>> missingClaim(MissingClaimException ex) {
        return ResponseEntity.status(HttpStatus.FORBIDDEN)
            .body(Map.of("error", "missing_claims", "message", ex.getMessage()));
    }
}
```

`src/main/java/cn/ouconline/ai/bridge/me/MeResponse.java`:

```java
package cn.ouconline.ai.bridge.me;

public record MeResponse(String userId, String userName, String name,
                         String nickname, String avatar, String tenantId) {
}
```

`src/main/java/cn/ouconline/ai/bridge/me/MeController.java`:

```java
package cn.ouconline.ai.bridge.me;

import cn.ouconline.ai.bridge.auth.PassportClaims;
import org.springframework.security.core.annotation.AuthenticationPrincipal;
import org.springframework.security.oauth2.jwt.Jwt;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
public class MeController {

    @GetMapping("/api/me")
    public MeResponse me(@AuthenticationPrincipal Jwt jwt) {
        PassportClaims c = PassportClaims.from(jwt);
        return new MeResponse(c.userId(), c.userName(), c.name(),
            c.nickname(), c.avatar(), c.tenantId());
    }
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `mvn test -Dtest=MeControllerTest`
Expected: `Tests run: 3, Failures: 0`。

- [ ] **Step 5: 全量回归 + Commit**

Run: `mvn test` → 全绿。

```powershell
git add src
git commit -m "feat: 资源服务器安全配置、异常映射与 /api/me"
```

---

### Task 5: ProxyService + GET /v1/models 转发

**Files:**
- Create: `src\main\java\cn\ouconline\ai\bridge\config\BridgeProperties.java`
- Create: `src\main\java\cn\ouconline\ai\bridge\proxy\UpstreamException.java`
- Create: `src\main\java\cn\ouconline\ai\bridge\proxy\ProxyService.java`
- Create: `src\main\java\cn\ouconline\ai\bridge\proxy\ProxyController.java`
- Modify: `src\main\java\cn\ouconline\ai\bridge\ApiExceptionHandler.java`（加 UpstreamException handler）
- Test: `src\test\java\cn\ouconline\ai\bridge\proxy\ModelsProxyTest.java`
- Test: `src\test\java\cn\ouconline\ai\bridge\proxy\UpstreamErrorTest.java`

**Interfaces:**
- Consumes: `PassportClaims`（Task 2）、`AiCoreAuthorizationBuilder.build(PassportClaims)`（Task 3）
- Produces: `BridgeProperties(String baseUrl, Duration connectTimeout, Duration responseTimeout)`（prefix `bridge.aicore`）；`ProxyService.forwardGet(String path, PassportClaims claims, HttpServletResponse response)` 与 `ProxyService.forwardPost(String path, byte[] jsonBody, PassportClaims claims, HttpServletResponse response)`（Task 6/7 消费）；`UpstreamException(String message, boolean timeout, Throwable cause)` + `isTimeout()`。

- [ ] **Step 1: 写失败测试**

`src/test/java/cn/ouconline/ai/bridge/proxy/ModelsProxyTest.java`:

```java
package cn.ouconline.ai.bridge.proxy;

import okhttp3.mockwebserver.MockResponse;
import okhttp3.mockwebserver.MockWebServer;
import okhttp3.mockwebserver.RecordedRequest;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.AutoConfigureMockMvc;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.security.oauth2.jwt.JwtDecoder;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;
import org.springframework.test.context.bean.override.mockito.MockitoBean;
import org.springframework.test.web.servlet.MockMvc;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.ObjectMapper;

import static org.assertj.core.api.Assertions.assertThat;
import static org.springframework.security.test.web.servlet.request.SecurityMockMvcRequestPostProcessors.jwt;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.content;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

@SpringBootTest
@AutoConfigureMockMvc
class ModelsProxyTest {

    // 静态块启动，确保先于 @DynamicPropertySource 求值
    static final MockWebServer aiCore = new MockWebServer();
    static {
        try {
            aiCore.start();
        } catch (Exception e) {
            throw new IllegalStateException(e);
        }
    }

    @AfterAll
    static void stop() throws Exception {
        aiCore.shutdown();
    }

    @DynamicPropertySource
    static void props(DynamicPropertyRegistry registry) {
        registry.add("bridge.aicore.base-url", () -> aiCore.url("/").toString());
    }

    @Autowired
    MockMvc mockMvc;

    @MockitoBean
    JwtDecoder jwtDecoder;

    final ObjectMapper mapper = new ObjectMapper();

    @Test
    void forwardsModelsWithConstructedIdentity() throws Exception {
        aiCore.enqueue(new MockResponse()
            .setHeader("Content-Type", "application/json")
            .setBody("{\"data\":[{\"id\":\"qwen-max\",\"owned_by\":\"qwen\"}]}"));

        mockMvc.perform(get("/v1/models").with(jwt().jwt(j -> j
                .claim("TenantId", "t-1").claim("UserId", "u-1").claim("Name", "张三"))))
            .andExpect(status().isOk())
            .andExpect(content().contentTypeCompatibleWith("application/json"))
            .andExpect(jsonPath("$.data[0].id").value("qwen-max"));

        RecordedRequest recorded = aiCore.takeRequest();
        assertThat(recorded.getPath()).isEqualTo("/v1/models");
        String auth = recorded.getHeader("Authorization");
        assertThat(auth).startsWith("Bearer {");
        JsonNode node = mapper.readTree(auth.substring("Bearer ".length()));
        assertThat(node.get("TenantId").asText()).isEqualTo("t-1");
        assertThat(node.get("DependentType").asText()).isEqualTo("User");
        assertThat(node.get("DependentId").asText()).isEqualTo("u-1");
        assertThat(node.get("DependentName").asText()).isEqualTo("%E5%BC%A0%E4%B8%89");
    }

    @Test
    void modelsRequiresAuthentication() throws Exception {
        mockMvc.perform(get("/v1/models")).andExpect(status().isUnauthorized());
    }
}
```

`src/test/java/cn/ouconline/ai/bridge/proxy/UpstreamErrorTest.java`:

```java
package cn.ouconline.ai.bridge.proxy;

import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.AutoConfigureMockMvc;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.security.oauth2.jwt.JwtDecoder;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;
import org.springframework.test.context.bean.override.mockito.MockitoBean;
import org.springframework.test.web.servlet.MockMvc;

import static org.springframework.security.test.web.servlet.request.SecurityMockMvcRequestPostProcessors.jwt;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

@SpringBootTest
@AutoConfigureMockMvc
class UpstreamErrorTest {

    @DynamicPropertySource
    static void props(DynamicPropertyRegistry registry) {
        // 端口 1 属保留端口，连接必被拒绝且失败得快
        registry.add("bridge.aicore.base-url", () -> "http://127.0.0.1:1");
    }

    @Autowired
    MockMvc mockMvc;

    @MockitoBean
    JwtDecoder jwtDecoder;

    @Test
    void unreachableUpstreamYields502() throws Exception {
        mockMvc.perform(get("/v1/models").with(jwt().jwt(j -> j
                .claim("TenantId", "t-1").claim("UserId", "u-1"))))
            .andExpect(status().isBadGateway())
            .andExpect(jsonPath("$.error").value("upstream_unreachable"));
    }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `mvn test -Dtest=ModelsProxyTest`
Expected: 编译错误（`ProxyController`、`ProxyService` 等不存在）。

- [ ] **Step 3: 最小实现**

`src/main/java/cn/ouconline/ai/bridge/config/BridgeProperties.java`:

```java
package cn.ouconline.ai.bridge.config;

import org.springframework.boot.context.properties.ConfigurationProperties;

import java.time.Duration;

/** AI.Core 上游连接配置。 */
@ConfigurationProperties(prefix = "bridge.aicore")
public record BridgeProperties(String baseUrl, Duration connectTimeout, Duration responseTimeout) {

    public BridgeProperties {
        if (connectTimeout == null) connectTimeout = Duration.ofSeconds(10);
        if (responseTimeout == null) responseTimeout = Duration.ofMinutes(5);
    }
}
```

`src/main/java/cn/ouconline/ai/bridge/proxy/UpstreamException.java`:

```java
package cn.ouconline.ai.bridge.proxy;

/** 调用 AI.Core 失败（连接失败或首包超时）。 */
public class UpstreamException extends RuntimeException {

    private final boolean timeout;

    public UpstreamException(String message, boolean timeout, Throwable cause) {
        super(message, cause);
        this.timeout = timeout;
    }

    public boolean isTimeout() {
        return timeout;
    }
}
```

`src/main/java/cn/ouconline/ai/bridge/proxy/ProxyService.java`:

```java
package cn.ouconline.ai.bridge.proxy;

import cn.ouconline.ai.bridge.auth.PassportClaims;
import cn.ouconline.ai.bridge.config.BridgeProperties;
import jakarta.servlet.http.HttpServletResponse;
import org.springframework.stereotype.Service;

import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.net.http.HttpTimeoutException;

/**
 * 把已认证请求翻译成 AI.Core 认可的身份后转发。
 * 响应逐块拷贝并即时 flush——SSE 流式输出依赖这一点，禁止整体缓冲。
 */
@Service
public class ProxyService {

    private static final int COPY_BUFFER_SIZE = 8 * 1024;

    private final BridgeProperties properties;
    private final HttpClient httpClient;

    public ProxyService(BridgeProperties properties) {
        this.properties = properties;
        this.httpClient = HttpClient.newBuilder()
            .connectTimeout(properties.connectTimeout())
            .followRedirects(HttpClient.Redirect.NEVER)
            .build();
    }

    public void forwardGet(String path, PassportClaims claims, HttpServletResponse response) {
        HttpRequest request = requestBuilder(path, claims).GET().build();
        forward(request, response);
    }

    public void forwardPost(String path, byte[] jsonBody, PassportClaims claims,
                            HttpServletResponse response) {
        HttpRequest request = requestBuilder(path, claims)
            .header("Content-Type", "application/json")
            .POST(HttpRequest.BodyPublishers.ofByteArray(jsonBody))
            .build();
        forward(request, response);
    }

    private HttpRequest.Builder requestBuilder(String path, PassportClaims claims) {
        String base = properties.baseUrl().endsWith("/")
            ? properties.baseUrl().substring(0, properties.baseUrl().length() - 1)
            : properties.baseUrl();
        return HttpRequest.newBuilder()
            .uri(URI.create(base + path))
            // JDK HttpClient 的 request timeout 只覆盖到响应头到达，
            // 不限制之后的流式 body 读取，对 SSE 长响应安全
            .timeout(properties.responseTimeout())
            .header("Authorization", AiCoreAuthorizationBuilder.build(claims));
    }

    private void forward(HttpRequest request, HttpServletResponse response) {
        HttpResponse<InputStream> upstream;
        try {
            upstream = httpClient.send(request, HttpResponse.BodyHandlers.ofInputStream());
        } catch (HttpTimeoutException e) {
            throw new UpstreamException("AI.Core 响应超时", true, e);
        } catch (IOException e) {
            throw new UpstreamException("无法连接 AI.Core: " + e.getMessage(), false, e);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new UpstreamException("转发被中断", false, e);
        }

        response.setStatus(upstream.statusCode());
        upstream.headers().firstValue("Content-Type").ifPresent(response::setContentType);

        try (InputStream in = upstream.body(); OutputStream out = response.getOutputStream()) {
            byte[] buffer = new byte[COPY_BUFFER_SIZE];
            int read;
            while ((read = in.read(buffer)) != -1) {
                out.write(buffer, 0, read);
                out.flush(); // 每块即时送达客户端
            }
        } catch (IOException e) {
            // 响应可能已部分写出（或客户端断开），无法再改状态码，只能中止
            throw new UpstreamException("转发流中断: " + e.getMessage(), false, e);
        }
    }
}
```

`src/main/java/cn/ouconline/ai/bridge/proxy/ProxyController.java`:

```java
package cn.ouconline.ai.bridge.proxy;

import cn.ouconline.ai.bridge.auth.PassportClaims;
import jakarta.servlet.http.HttpServletResponse;
import org.springframework.security.core.annotation.AuthenticationPrincipal;
import org.springframework.security.oauth2.jwt.Jwt;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
public class ProxyController {

    private final ProxyService proxyService;

    public ProxyController(ProxyService proxyService) {
        this.proxyService = proxyService;
    }

    @GetMapping("/v1/models")
    public void models(@AuthenticationPrincipal Jwt jwt, HttpServletResponse response) {
        proxyService.forwardGet("/v1/models", PassportClaims.from(jwt), response);
    }
}
```

`ApiExceptionHandler.java` 增加（放在 `missingClaim` 方法之后）：

```java
    @ExceptionHandler(UpstreamException.class)
    ResponseEntity<Map<String, String>> upstream(UpstreamException ex) {
        HttpStatus status = ex.isTimeout() ? HttpStatus.GATEWAY_TIMEOUT : HttpStatus.BAD_GATEWAY;
        return ResponseEntity.status(status)
            .body(Map.of(
                "error", ex.isTimeout() ? "upstream_timeout" : "upstream_unreachable",
                "message", ex.getMessage()));
    }
```

同时在文件头补 import：`cn.ouconline.ai.bridge.proxy.UpstreamException`。

- [ ] **Step 4: 跑测试确认通过**

Run: `mvn test -Dtest=ModelsProxyTest,UpstreamErrorTest`
Expected: `Tests run: 3, Failures: 0`。

- [ ] **Step 5: 全量回归 + Commit**

Run: `mvn test` → 全绿。

```powershell
git add src
git commit -m "feat: /v1/models 身份翻译转发与上游错误映射"
```

---

### Task 6: POST /v1/chat/completions（非流式）+ user 字段注入

**Files:**
- Create: `src\main\java\cn\ouconline\ai\bridge\proxy\InvalidRequestBodyException.java`
- Modify: `src\main\java\cn\ouconline\ai\bridge\proxy\ProxyController.java`（加 POST 端点）
- Modify: `src\main\java\cn\ouconline\ai\bridge\ApiExceptionHandler.java`（加 400 handler）
- Test: `src\test\java\cn\ouconline\ai\bridge\proxy\ChatProxyTest.java`

**Interfaces:**
- Consumes: `ProxyService.forwardPost(...)`（Task 5）、`PassportClaims`（Task 2）
- Produces: `POST /v1/chat/completions`（body 里 `user` 字段被覆写为 JWT 的 UserId 后转发）；非法请求体 → 400 `{"error":"invalid_request"}`。

- [ ] **Step 1: 写失败测试**

`src/test/java/cn/ouconline/ai/bridge/proxy/ChatProxyTest.java`:

```java
package cn.ouconline.ai.bridge.proxy;

import okhttp3.mockwebserver.MockResponse;
import okhttp3.mockwebserver.MockWebServer;
import okhttp3.mockwebserver.RecordedRequest;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.AutoConfigureMockMvc;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.http.MediaType;
import org.springframework.security.oauth2.jwt.JwtDecoder;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;
import org.springframework.test.context.bean.override.mockito.MockitoBean;
import org.springframework.test.web.servlet.MockMvc;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.ObjectMapper;

import static org.assertj.core.api.Assertions.assertThat;
import static org.springframework.security.test.web.servlet.request.SecurityMockMvcRequestPostProcessors.jwt;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

@SpringBootTest
@AutoConfigureMockMvc
class ChatProxyTest {

    static final MockWebServer aiCore = new MockWebServer();
    static {
        try {
            aiCore.start();
        } catch (Exception e) {
            throw new IllegalStateException(e);
        }
    }

    @AfterAll
    static void stop() throws Exception {
        aiCore.shutdown();
    }

    @DynamicPropertySource
    static void props(DynamicPropertyRegistry registry) {
        registry.add("bridge.aicore.base-url", () -> aiCore.url("/").toString());
    }

    @Autowired
    MockMvc mockMvc;

    @MockitoBean
    JwtDecoder jwtDecoder;

    final ObjectMapper mapper = new ObjectMapper();

    @Test
    void forwardsChatAndInjectsUser() throws Exception {
        aiCore.enqueue(new MockResponse()
            .setHeader("Content-Type", "application/json")
            .setBody("{\"id\":\"cmpl-1\",\"choices\":[]}"));

        mockMvc.perform(post("/v1/chat/completions")
                .contentType(MediaType.APPLICATION_JSON)
                .content("{\"model\":\"qwen-max\",\"messages\":[{\"role\":\"user\",\"content\":\"你好\"}]}")
                .with(jwt().jwt(j -> j.claim("TenantId", "t-1").claim("UserId", "u-1"))))
            .andExpect(status().isOk())
            .andExpect(jsonPath("$.id").value("cmpl-1"));

        RecordedRequest recorded = aiCore.takeRequest();
        assertThat(recorded.getPath()).isEqualTo("/v1/chat/completions");
        JsonNode sent = mapper.readTree(recorded.getBody().readUtf8());
        assertThat(sent.get("user").asText()).isEqualTo("u-1"); // AI.Core 会话日志按此记 UserId
        assertThat(sent.get("model").asText()).isEqualTo("qwen-max");
        assertThat(sent.get("messages").get(0).get("content").asText()).isEqualTo("你好");
    }

    @Test
    void overwritesClientProvidedUser() throws Exception {
        aiCore.enqueue(new MockResponse()
            .setHeader("Content-Type", "application/json")
            .setBody("{}"));

        mockMvc.perform(post("/v1/chat/completions")
                .contentType(MediaType.APPLICATION_JSON)
                .content("{\"model\":\"m\",\"user\":\"spoofed\",\"messages\":[]}")
                .with(jwt().jwt(j -> j.claim("TenantId", "t-1").claim("UserId", "u-1"))))
            .andExpect(status().isOk());

        RecordedRequest recorded = aiCore.takeRequest();
        JsonNode sent = mapper.readTree(recorded.getBody().readUtf8());
        assertThat(sent.get("user").asText()).isEqualTo("u-1"); // 服务端权威，不信客户端
    }

    @Test
    void invalidJsonBodyYields400() throws Exception {
        mockMvc.perform(post("/v1/chat/completions")
                .contentType(MediaType.APPLICATION_JSON)
                .content("not-json")
                .with(jwt().jwt(j -> j.claim("TenantId", "t-1").claim("UserId", "u-1"))))
            .andExpect(status().isBadRequest())
            .andExpect(jsonPath("$.error").value("invalid_request"));
    }

    @Test
    void nonObjectJsonBodyYields400() throws Exception {
        mockMvc.perform(post("/v1/chat/completions")
                .contentType(MediaType.APPLICATION_JSON)
                .content("[1,2,3]")
                .with(jwt().jwt(j -> j.claim("TenantId", "t-1").claim("UserId", "u-1"))))
            .andExpect(status().isBadRequest())
            .andExpect(jsonPath("$.error").value("invalid_request"));
    }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `mvn test -Dtest=ChatProxyTest`
Expected: FAIL —— `/v1/chat/completions` 404（POST 端点不存在）。

- [ ] **Step 3: 最小实现**

`src/main/java/cn/ouconline/ai/bridge/proxy/InvalidRequestBodyException.java`:

```java
package cn.ouconline.ai.bridge.proxy;

/** 请求体不是合法的 JSON 对象。 */
public class InvalidRequestBodyException extends RuntimeException {
    public InvalidRequestBodyException(String message) {
        super(message);
    }
}
```

`ProxyController.java` 改为：

```java
package cn.ouconline.ai.bridge.proxy;

import cn.ouconline.ai.bridge.auth.PassportClaims;
import jakarta.servlet.http.HttpServletResponse;
import org.springframework.security.core.annotation.AuthenticationPrincipal;
import org.springframework.security.oauth2.jwt.Jwt;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RestController;
import tools.jackson.core.JacksonException;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.ObjectMapper;
import tools.jackson.databind.node.ObjectNode;

@RestController
public class ProxyController {

    private static final ObjectMapper MAPPER = new ObjectMapper();

    private final ProxyService proxyService;

    public ProxyController(ProxyService proxyService) {
        this.proxyService = proxyService;
    }

    @GetMapping("/v1/models")
    public void models(@AuthenticationPrincipal Jwt jwt, HttpServletResponse response) {
        proxyService.forwardGet("/v1/models", PassportClaims.from(jwt), response);
    }

    @PostMapping("/v1/chat/completions")
    public void chatCompletions(@AuthenticationPrincipal Jwt jwt,
                                @RequestBody String body,
                                HttpServletResponse response) {
        PassportClaims claims = PassportClaims.from(jwt);
        ObjectNode json = parseObject(body);
        json.put("user", claims.userId()); // 服务端权威：AI.Core 会话日志按此记 UserId
        byte[] payload = MAPPER.writeValueAsBytes(json);
        proxyService.forwardPost("/v1/chat/completions", payload, claims, response);
    }

    private static ObjectNode parseObject(String body) {
        JsonNode tree;
        try {
            tree = MAPPER.readTree(body);
        } catch (JacksonException e) {
            throw new InvalidRequestBodyException("请求体不是合法 JSON");
        }
        if (tree == null || !tree.isObject()) {
            throw new InvalidRequestBodyException("请求体必须是 JSON 对象");
        }
        return (ObjectNode) tree;
    }
}
```

`ApiExceptionHandler.java` 增加：

```java
    @ExceptionHandler(InvalidRequestBodyException.class)
    ResponseEntity<Map<String, String>> invalidBody(InvalidRequestBodyException ex) {
        return ResponseEntity.status(HttpStatus.BAD_REQUEST)
            .body(Map.of("error", "invalid_request", "message", ex.getMessage()));
    }
```

同时补 import：`cn.ouconline.ai.bridge.proxy.InvalidRequestBodyException`。

- [ ] **Step 4: 跑测试确认通过**

Run: `mvn test -Dtest=ChatProxyTest`
Expected: `Tests run: 4, Failures: 0`。

- [ ] **Step 5: 全量回归 + Commit**

Run: `mvn test` → 全绿。

```powershell
git add src
git commit -m "feat: /v1/chat/completions 转发并以 JWT UserId 覆写 user 字段"
```

---

### Task 7: SSE 流式透传（端到端真 HTTP 测试）

**Files:**
- Test: `src\test\java\cn\ouconline\ai\bridge\proxy\ChatStreamingProxyTest.java`
- （若测试暴露缓冲问题才需改 `ProxyService.java`——按 Task 5 的 flush-per-read 实现应直接通过）

**Interfaces:**
- Consumes: Task 5/6 的完整链路
- Produces: 已验证的 SSE 透传行为（`text/event-stream` 头保留、chunk 内容零改动、`data: [DONE]` 终止）

- [ ] **Step 1: 写测试**

`src/test/java/cn/ouconline/ai/bridge/proxy/ChatStreamingProxyTest.java`:

```java
package cn.ouconline.ai.bridge.proxy;

import okhttp3.mockwebserver.MockResponse;
import okhttp3.mockwebserver.MockWebServer;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.Test;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.boot.test.context.TestConfiguration;
import org.springframework.boot.web.server.test.LocalServerPort;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Primary;
import org.springframework.security.oauth2.jwt.Jwt;
import org.springframework.security.oauth2.jwt.JwtDecoder;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Instant;

import static org.assertj.core.api.Assertions.assertThat;

@SpringBootTest(webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT)
class ChatStreamingProxyTest {

    static final MockWebServer aiCore = new MockWebServer();
    static {
        try {
            aiCore.start();
        } catch (Exception e) {
            throw new IllegalStateException(e);
        }
    }

    @AfterAll
    static void stop() throws Exception {
        aiCore.shutdown();
    }

    @DynamicPropertySource
    static void props(DynamicPropertyRegistry registry) {
        registry.add("bridge.aicore.base-url", () -> aiCore.url("/").toString());
    }

    /** 真 HTTP 端口上没法用 MockMvc 的 jwt()，用桩解码器放行任意 Bearer 并给定 claims。 */
    @TestConfiguration
    static class StubJwtDecoderConfig {
        @Bean
        @Primary
        JwtDecoder stubJwtDecoder() {
            return token -> Jwt.withTokenValue(token)
                .header("alg", "none")
                .subject("zhangsan")
                .claim("TenantId", "t-1").claim("UserId", "u-1").claim("Name", "张三")
                .issuedAt(Instant.now()).expiresAt(Instant.now().plusSeconds(300))
                .build();
        }
    }

    @LocalServerPort
    int port;

    @Test
    void streamsSsePassthroughUnchanged() throws Exception {
        String sse = "data: {\"id\":\"c1\",\"choices\":[{\"delta\":{\"content\":\"你\"}}]}\n\n"
                   + "data: {\"id\":\"c1\",\"choices\":[{\"delta\":{\"content\":\"好\"}}]}\n\n"
                   + "data: [DONE]";
        aiCore.enqueue(new MockResponse()
            .setHeader("Content-Type", "text/event-stream")
            .setChunkedBody(sse, 16)); // 小 chunk 模拟逐字流

        HttpClient client = HttpClient.newHttpClient();
        HttpRequest request = HttpRequest.newBuilder()
            .uri(URI.create("http://127.0.0.1:" + port + "/v1/chat/completions"))
            .header("Authorization", "Bearer anything")
            .header("Content-Type", "application/json")
            .POST(HttpRequest.BodyPublishers.ofString(
                "{\"model\":\"qwen-max\",\"stream\":true,\"messages\":[]}"))
            .build();
        HttpResponse<String> response = client.send(request, HttpResponse.BodyHandlers.ofString());

        assertThat(response.statusCode()).isEqualTo(200);
        assertThat(response.headers().firstValue("Content-Type").orElse(""))
            .contains("text/event-stream");
        // 透传零改动：字节级一致（ProxyService flush-per-read 保证逐块送达）
        assertThat(response.body()).isEqualTo(sse);
    }

    @Test
    void invalidTokenStillHits401OnRealPort() throws Exception {
        // 不带 Authorization 头：安全过滤器直接拒绝，不经过桩解码器
        HttpClient client = HttpClient.newHttpClient();
        HttpRequest request = HttpRequest.newBuilder()
            .uri(URI.create("http://127.0.0.1:" + port + "/v1/models"))
            .GET()
            .build();
        HttpResponse<String> response = client.send(request, HttpResponse.BodyHandlers.ofString());
        assertThat(response.statusCode()).isEqualTo(401);
    }
}
```

- [ ] **Step 2: 跑测试**

Run: `mvn test -Dtest=ChatStreamingProxyTest`
Expected: `Tests run: 2, Failures: 0`（Task 5 的实现本就逐块 flush，应直接通过）。
若 `@LocalServerPort` import 编译失败，按"版本适配说明"换成 `org.springframework.boot.test.web.server.LocalServerPort`。
若 body 不一致（多了/少了字节），说明中间有缓冲或改写——检查 `ProxyService.forward` 是否有 flush、是否误设了 Content-Length。

- [ ] **Step 3: 全量回归 + Commit**

Run: `mvn test` → 全绿。

```powershell
git add src
git commit -m "test: SSE 流式透传端到端验证（真 HTTP 端口）"
```

---

### Task 8: Dockerfile、K8s 清单与 README

**Files:**
- Create: `D:\ouconline\ouconline-ai-bridge\Dockerfile`
- Create: `D:\ouconline\ouconline-ai-bridge\yamls\bridge.yaml`
- Create: `D:\ouconline\ouconline-ai-bridge\README.md`

**Interfaces:**
- Consumes: Task 1-7 的完整可构建工程
- Produces: 可交付的容器镜像构建/部署材料与对接文档

- [ ] **Step 1: 写 Dockerfile**

```dockerfile
FROM maven:3.9-eclipse-temurin-21 AS build
WORKDIR /build
COPY pom.xml .
RUN mvn -q dependency:go-offline
COPY src ./src
RUN mvn -q -DskipTests package

FROM eclipse-temurin:21-jre
WORKDIR /app
COPY --from=build /build/target/ouconline-ai-bridge-*.jar app.jar
EXPOSE 8080
ENTRYPOINT ["java", "-jar", "app.jar"]
```

- [ ] **Step 2: 写 K8s 清单**

`yamls/bridge.yaml`（镜像地址按实际镜像仓库替换；环境变量按目标环境替换）：

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ouconline-ai-bridge
  labels:
    app: ouconline-ai-bridge
spec:
  replicas: 2
  selector:
    matchLabels:
      app: ouconline-ai-bridge
  template:
    metadata:
      labels:
        app: ouconline-ai-bridge
    spec:
      containers:
        - name: ouconline-ai-bridge
          image: registry.example.com/ouconline/ouconline-ai-bridge:0.1.0 # 按实际仓库替换
          ports:
            - containerPort: 8080
          env:
            - name: BRIDGE_PASSPORT_ISSUER_URI
              value: "https://passport.le.ouc-online.com.cn/" # 按实际 Passport issuer 替换
            - name: BRIDGE_AICORE_BASE_URL
              value: "http://aicore-service" # 按 AI.Core 集群内 Service 地址替换
          readinessProbe:
            httpGet:
              path: /actuator/health
              port: 8080
            initialDelaySeconds: 10
          livenessProbe:
            httpGet:
              path: /actuator/health
              port: 8080
            initialDelaySeconds: 20
---
apiVersion: v1
kind: Service
metadata:
  name: ouconline-ai-bridge
spec:
  selector:
    app: ouconline-ai-bridge
  ports:
    - port: 80
      targetPort: 8080
```

- [ ] **Step 3: 写 README.md**

内容必须包含以下小节（照抄骨架，补齐命令输出无需照抄）：

```markdown
# ouconline-ai-bridge

Passport(IdentityServer4) JWT → AI.Core 租户身份的翻译代理。桌面客户端携带
Passport 访问令牌调用本服务的 OpenAI 兼容端点，本服务验签后以
`{"TenantId":…,"DependentType":"User","DependentId":…,"DependentName":…}`
的 JSON Bearer 转发给 AI.Core（SSE 流式透传）。

## 端点

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | /v1/models | 转发 AI.Core 模型列表 |
| POST | /v1/chat/completions | 转发对话（流式/非流式），`user` 字段以 JWT UserId 覆写 |
| GET | /api/me | 从令牌 claims 返回用户信息（不回源） |
| GET | /actuator/health | 健康检查（匿名） |

## 配置（环境变量）

| 变量 | 默认 | 说明 |
|---|---|---|
| BRIDGE_PASSPORT_ISSUER_URI | http://localhost:5000 | Passport issuer（须与令牌 iss 完全一致） |
| BRIDGE_AICORE_BASE_URL | http://localhost:5100 | AI.Core 根地址（不含 /v1） |

## 本地运行

mvn spring-boot:run

## 验证

# 无令牌 → 401
curl -i http://localhost:8080/v1/models
# 携带 Passport 签发的真实令牌 → 转发结果
curl -i -H "Authorization: Bearer <access_token>" http://localhost:8080/v1/models

## 前置要求

- 客户端申请的 scope 必须包含带用户声明的 API scope（如 passportapi），
  否则令牌缺少 TenantId/UserId，本服务返回 403 missing_claims。
- 本服务必须部署在 AI.Core 的受信网络内（JSON Bearer 为信任模式）。
```

- [ ] **Step 4: 构建验证**

Run: `mvn verify`
Expected: `BUILD SUCCESS`，全部测试通过。
（有 Docker 环境则可选：`docker build -t ouconline-ai-bridge:dev .` 应成功。）

- [ ] **Step 5: Commit**

```powershell
git add Dockerfile yamls README.md
git commit -m "chore: Dockerfile、K8s 清单与对接 README"
```

---

## Self-Review 结果

- **Spec 覆盖**：SecurityConfig/JWKS 验签（Task 4）、JSON Bearer 构造含中文 URL 编码（Task 3）、/v1/models 与 /v1/chat/completions 转发（Task 5/6）、SSE 逐块透传不缓冲（Task 5 实现 + Task 7 验证）、user 字段注入（Task 6）、/api/me 不回源（Task 4）、401/403/400/502/504 错误语义（Task 4/5/6）、AI.Core 响应原样透传（forward 不改 body）、无状态无数据库（全程未引入）、Dockerfile/K8s/README（Task 8）。未覆盖项：无。
- **占位符扫描**：K8s 清单中镜像地址与环境变量为部署时必须替换的环境值，已逐一标注"按实际替换"；代码与测试无 TBD/省略。
- **类型一致性**：`PassportClaims.from(Jwt)`（Task 2 定义，Task 4/5/6 消费）、`AiCoreAuthorizationBuilder.build(PassportClaims)`（Task 3 定义，Task 5 消费）、`ProxyService.forwardGet/forwardPost` 签名（Task 5 定义，Task 6 消费）、`UpstreamException(String, boolean, Throwable)` + `isTimeout()`（Task 5 定义与消费）——已核对一致。
