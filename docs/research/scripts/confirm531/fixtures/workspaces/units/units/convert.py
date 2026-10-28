"""Unit conversions used by the trip planner."""


def miles_to_km(miles: float) -> float:
    """Convert statute miles to kilometres."""
    return miles * 1.609344


def feet_to_m(feet: float) -> float:
    """Convert feet to metres."""
    return feet * 0.3048
