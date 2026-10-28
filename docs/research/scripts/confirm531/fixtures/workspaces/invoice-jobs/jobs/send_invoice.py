"""Job: render an invoice and send it to the customer."""


def send_invoice(payload: dict) -> None:
    pdf = payload["pdf_base64"]
    recipient = payload["email"]
    print(f"sending {len(pdf)} bytes to {recipient}")
