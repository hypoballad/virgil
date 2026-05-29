import os
import sys as system
from collections import defaultdict, namedtuple
from pathlib import Path as FilePath
from typing import Any, ClassVar
from .helpers import local_helper


def audit_decorator(fn):
    return fn


class AuditExample:
    kind: ClassVar[str] = "audit"

    def __init__(self, value: int) -> None:
        self.value = value

    @property
    def doubled(self) -> int:
        return self.value * 2

    @staticmethod
    def normalize(raw: str) -> str:
        return raw.strip().lower()

    @classmethod
    def build(cls, raw: str) -> "AuditExample":
        return cls(len(cls.normalize(raw)))

    @audit_decorator
    async def fetch(self, key: str) -> dict[str, Any]:
        payload = await load_payload(key)
        return {"key": key, "payload": payload}

    def call_everything(self) -> int:
        other = self.build(" value ")
        print(other.doubled)
        return other.doubled


async def load_payload(key: str) -> str:
    return f"payload:{key}"


def typed_function(name: str, count: int = 1) -> list[str]:
    return [AuditExample.normalize(name) for _ in range(count)]


def module_level() -> None:
    item = AuditExample.build(" module ")
    typed_function(str(item.doubled), 2)
    local_helper(system.version)
    FilePath(os.getcwd())
    defaultdict(list)
    namedtuple("Point", ["x", "y"])
