package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// AdminClaims 管理后台 JWT 声明
type AdminClaims struct {
	AdminID  uint   `json:"admin_id"`
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// SignJWT 签发管理后台 token，有效期 24h
func SignJWT(secret string, adminID uint, username string) (string, error) {
	claims := AdminClaims{
		AdminID:  adminID,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "deepseek-web-api",
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString([]byte(secret))
}

// ParseJWT 解析并校验 token
func ParseJWT(tokenStr, secret string) (*AdminClaims, error) {
	claims := &AdminClaims{}
	t, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	if !t.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

// HashPassword bcrypt 哈希
// (放在这里便于 admin / repository 共用，避免重复导入 bcrypt)

// VerifyPassword 校验
