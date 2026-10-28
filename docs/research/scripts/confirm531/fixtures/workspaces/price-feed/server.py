"""Price service: unary lookups today."""
import thistlewire

app = thistlewire.Server(port=7300)


@app.unary("prices.Get")
def get_price(request):
    return {"symbol": request["symbol"], "price": lookup(request["symbol"])}


def lookup(symbol: str) -> float:
    return 0.0
