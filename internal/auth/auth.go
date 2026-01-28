package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type User struct {
	ID        string
	GoogleSub string
	Email     string
	Name      string
	AvatarURL string
}

var userKey = &struct{}{}

func ContextWithUser(ctx context.Context, user User) context.Context {
	return context.WithValue(ctx, userKey, user)
}

func UserFromContext(ctx context.Context) (User, bool) {
	v := ctx.Value(userKey)
	if v == nil {
		return User{}, false
	}
	u, ok := v.(User)
	return u, ok
}

func RandomString(n int) (string, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func IssueJWT(secret, userID string, ttl time.Duration) (string, int64, error) {
	now := time.Now()
	exp := now.Add(ttl)
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID,
		"iat": now.Unix(),
		"exp": exp.Unix(),
	})
	s, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", 0, err
	}
	return s, exp.Unix(), nil
}

func ParseJWT(secret, tokenString string) (string, error) {
	parsed, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil || !parsed.Valid {
		return "", errors.New("invalid token")
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return "", errors.New("invalid claims")
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return "", errors.New("missing sub")
	}
	return sub, nil
}
