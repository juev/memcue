package policy

import (
	"testing"
)

func TestSecret(t *testing.T) {
	for text, want := range map[string]bool{
		"пароль от базы demo: hunter2":                   true,
		"Пароль: Qwerty-2026":                            true,
		"токен бота matrix = syt_abc123def456":           true,
		"секрет вебхука: wh_9f8e7d6c":                    true,
		"ключ API: AbCd1234EfGh":                         true,
		"password=hunter2secret":                         true,
		"api_key: sk_live_51Habc":                        true,
		"Токен ghp_abcdefghijklmnopqrstuvwxyz0123456789": true,
		"password=hunter":                                true,
		"пароль: qwerty":                                 true,
		"password=12345678":                              true,
		"PWD: abcd":                                      true,
		"secret: qwerty":                                 true,
		"секрет вебхука: qwerty":                         true,
		"секрет: qwerty.":                                true,
		"секрет: qwerty":                                 true,
		"токен: abcdef":                                  true,
		"api_key: AbCdEfGh":                              true,
		"ключ доступа: abcd":                             true,
		"password: Ab!!":                                 true,
		"secret: Ab!!":                                   true,
		"token: AbCd==":                                  true,
		"token: abcde=":                                  true,
		"token: 1500":                                    false,
		"токен: abc":                                     false,
		"пароль: 123":                                    false,
		"password authentication failed for user":        false,
		"пароль хранится в 1Password, не в репозитории": false,
		"token budget: 150000":                             false,
		"ключевые слова: порт, база":                       false,
		"секрет не в коде, а в сообщении коммита":          false,
		"api key rotation is monthly":                      false,
		"Пароль от Wi-Fi спросить у пользователя: не знаю": false,
	} {
		if got := Secret(text); got != want {
			t.Errorf("Secret(%q) = %v, want %v", text, got, want)
		}
	}
}
