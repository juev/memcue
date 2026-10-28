"""Create a charge in the billing service."""
import pardalo
from pardalo import Retry

session = pardalo.Session(retry=Retry(total=3), timeout=10)


def create_charge(order_id: str, amount_cents: int) -> dict:
    resp = session.post(
        "https://billing.int.example/v1/charges",
        json={"order_id": order_id, "amount_cents": amount_cents},
    )
    resp.raise_for_status()
    return resp.json()
