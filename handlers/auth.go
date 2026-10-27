package handlers

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"feed/services"
)

// Auth JWT 鉴权中间件，验证通过才放行
func Auth(c *gin.Context) {
	tokenString := c.GetHeader("Authorization")
	if tokenString == "" {
		c.JSON(401, gin.H{"error": "未登录"})
		c.Abort()
		return
	}

	// 解析并验证签名
	token, err := parseToken(tokenString)
	if err != nil || !token.Valid {
		c.JSON(401, gin.H{"error": "token 无效"})
		c.Abort()
		return
	}

	// 用户 id 存入上下文，供后续 handler 使用
	setUserID(c, token)

	c.Next()
}

// SoftAuth 公共接口的可选认证：带 token 就解析身份，没带按游客放行。
// 带了 token 但验签失败直接拒绝，不静默降级成游客——客户端带着失效凭证却看到
// 未登录的内容，比明确报错更难排查。
func SoftAuth(c *gin.Context) {
	tokenString := c.GetHeader("Authorization")
	if tokenString == "" {
		c.Next()
		return
	}

	token, err := parseToken(tokenString)
	if err != nil || !token.Valid {
		c.JSON(401, gin.H{"error": "token 无效"})
		c.Abort()
		return
	}

	setUserID(c, token)
	c.Next()
}

// parseToken 解析并验证签名，Auth 与 SoftAuth 共用
func parseToken(tokenString string) (*jwt.Token, error) {
	return jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
		// 显式校验签名算法，只接受服务端签发时使用的 HMAC 系列。
		// 注意：golang-jwt v5 对 alg:none 及算法混淆本身已有防护（none 需要显式的
		// UnsafeAllowNoneSignatureType 才放行，换算法会因密钥类型不符而校验失败），
		// 这里按官方建议显式声明允许的算法，避免依赖库的隐式行为、也便于日后扩展。
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return services.Secret(), nil
	})
}

// setUserID 把 token 里的用户 id 写入上下文
func setUserID(c *gin.Context, token *jwt.Token) {
	claims := token.Claims.(jwt.MapClaims)
	c.Set("user_id", fmt.Sprintf("%.0f", claims["user_id"].(float64)))
}
