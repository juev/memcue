package extract

import (
	"context"
	"errors"
)

// dreamRunSystem is the system message of a dream packet sent to a model: the packet itself carries the instructions,
// the four tasks, the data and the form of the answer.
const dreamRunSystem = "Ты — модель memcue dream. Сообщение пользователя — пакет работы целиком: инструкции, задачи и " +
	"данные. Выполни его и верни ровно тот JSON-объект, который описан в разделе «Форма ответа», без пояснений и без " +
	"markdown."

// dreamSchema is the form of a dream answer for `claude -p`, which holds its output to a JSON schema.
const dreamSchema = `{"type":"object","additionalProperties":false,
"required":["packet","splits","expansions","actions","translations"],"properties":{
"packet":{"type":"string"},
"splits":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["id","parts","reason"],
"properties":{"id":{"type":"string"},"parts":{"type":"array","items":{"type":"string"}},"reason":{"type":"string"}}}},
"expansions":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["id","queries"],
"properties":{"id":{"type":"string"},"queries":{"type":"array","items":{"type":"string"}}}}},
"actions":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["op","id","reason"],
"properties":{"op":{"type":"string","enum":["retire","correct"]},"id":{"type":"string"},"content":{"type":"string"},
"keywords":{"type":"array","items":{"type":"string"}},"queries":{"type":"array","items":{"type":"string"}},
"by":{"type":"string"},"kind":{"type":"string","enum":["duplicate","outdated"]},"reason":{"type":"string"}}}},
"translations":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["id","translation"],
"properties":{"id":{"type":"string"},"translation":{"type":"string"}}}}}}`

// ErrNoEndpoint: a plain memcue dream -run has no endpoint to send the packet to.
var ErrNoEndpoint = errors.New("no MEMCUE_API_URL: the dream runs on the session's model (memcue dream -prepare), " +
	"or on claude -p with memcue dream -run -all")

// DreamAnswer sends a dream packet to a model and returns its answer for memcue dream -apply with what the call cost:
// the configured endpoint when MEMCUE_API_URL is set (#401), `claude -p` on Model otherwise (#405). The `claude -p`
// call has no tools and no hooks, so the model answers the packet and cannot carry it out itself.
//
// Unlike the other calls of an endpoint it lets the model reason. A dream retires and rewrites facts with nobody to
// review them, and on two live packets deepseek-flash without reasoning retired a fact its "duplicate" did not cover,
// split a list of two domains into two facts, and on the larger packet cut its answer at the token limit, so nothing
// was applied. With reasoning both packets came back whole and right, for 22,000-29,000 output tokens and 94-137 s.
func DreamAnswer(ctx context.Context, packet string) ([]byte, float64, Tokens, error) {
	var rp reply
	var err error
	if API.URL != "" {
		rp, err = runAPI(ctx, Endpoint{URL: API.URL, Model: Model, Key: API.Key}, dreamRunSystem, packet)
	} else {
		rp, err = callModel(ctx, Endpoint{Model: Model}, dreamRunSystem, dreamSchema, "", packet)
	}
	if err != nil {
		return nil, rp.cost, rp.tokens, err
	}
	return rp.payload, rp.cost, rp.tokens, nil
}
