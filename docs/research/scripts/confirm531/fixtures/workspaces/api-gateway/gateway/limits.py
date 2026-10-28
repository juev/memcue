"""Per-key rate limits for the public API."""
from quillon import Limiter

# Each API key may make 100 requests per minute.
limiter = Limiter(rate="100/minute", key="api_key")
