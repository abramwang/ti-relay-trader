"""Independent offline protocol-vector oracle, not OC code or a ledger codec."""

from decimal import Decimal
import re
import struct


def encode_bits(bits, *, required, rounding_mode="FE_TONEAREST"):
    if bits is None:
        return {"error": "money_missing"} if required else {"value": None}
    if not isinstance(bits, str) or re.fullmatch(r"[0-9a-f]{16}", bits) is None:
        raise ValueError("expected big-endian 16-digit binary64 bit pattern")
    word = int(bits, 16)
    magnitude = word & ((1 << 63) - 1)
    # Classify signaling NaN before loading it into a floating-point value.
    if (word >> 52) & 0x7ff == 0x7ff:
        return {"error": "money_non_finite"}
    if magnitude == 0x7fefffffffffffff:
        return {"error": "money_suspected_sentinel"}
    if rounding_mode != "FE_TONEAREST":
        return {"error": "money_rounding_mode_unsupported"}
    number = struct.unpack(">d", bytes.fromhex(bits))[0]
    # 17 significant digits then exact decimal exponent expansion, never cents rounding.
    value = format(Decimal(format(number, ".17g")), "f")
    if "." in value:
        value = value.rstrip("0").rstrip(".")
    if len(value) > 343:
        return {"error": "money_text_too_long"}
    return {"value": value}
