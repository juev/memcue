package extract

import (
	"context"
	"errors"
)

// dreamRunSystem is the system message of a dream packet sent to an endpoint: the packet itself carries the
// instructions, the four tasks, the data and the form of the answer.
const dreamRunSystem = "Ты — модель memcue dream. Сообщение пользователя — пакет работы целиком: инструкции, задачи и " +
	"данные. Выполни его и верни ровно тот JSON-объект, который описан в разделе «Форма ответа», без пояснений и без " +
	"markdown."

// ErrNoEndpoint: memcue dream -run has no endpoint to send the packet to.
var ErrNoEndpoint = errors.New("no MEMCUE_API_URL: the dream runs on the session's model (memcue dream -prepare)")

// DreamAnswer sends a dream packet to the configured endpoint and returns the model's answer for memcue dream -apply
// with what the call cost (#401). It is for a host whose plugin has no session model to hand the packet to but already
// calls an endpoint for the extraction, as Hermes does; without MEMCUE_API_URL it refuses, because claude -p would
// start the session's model again, which the memcue-dream skill already is.
//
// Unlike the other calls of an endpoint it lets the model reason. A dream retires and rewrites facts with nobody to
// review them, and on two live packets deepseek-flash without reasoning retired a fact its "duplicate" did not cover,
// split a list of two domains into two facts, and on the larger packet cut its answer at the token limit, so nothing
// was applied. With reasoning both packets came back whole and right, for 22,000-29,000 output tokens and 94-137 s.
func DreamAnswer(ctx context.Context, packet string) ([]byte, Tokens, error) {
	if API.URL == "" {
		return nil, Tokens{}, ErrNoEndpoint
	}
	rp, err := runAPI(ctx, Endpoint{URL: API.URL, Model: Model, Key: API.Key}, dreamRunSystem, packet)
	if err != nil {
		return nil, rp.tokens, err
	}
	return rp.payload, rp.tokens, nil
}
