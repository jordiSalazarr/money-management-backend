package auth

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jordisalazar/money-management/internal/shared/middleware"
)

func GenerateToken(userID, secret string, now time.Time) (string, error) {
	claims := middleware.Claims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
			Issuer:    "money-management",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

func ValidateToken(tokenStr, secret string) (*middleware.Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &middleware.Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*middleware.Claims)
	if !ok {
		return nil, jwt.ErrTokenInvalidClaims
	}
	return claims, nil
}
