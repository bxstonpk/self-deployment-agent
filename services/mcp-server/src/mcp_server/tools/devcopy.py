"""Dev copies (DEC-029 part 3): each application's long-lived copy to try
changes on, "<name>-dev", with its own database and address.
deploy_application(target_environment="dev") deploys to it.

    record_test_data  what test data Claude itself put in the dev copy's database
    reset_dev         remove the dev copy, its code and its data
"""

from __future__ import annotations

from typing import Any

from ..envelope import success
from ..platform_client import PlatformClient


async def record_test_data(client: PlatformClient, application_id: str, note: str) -> dict[str, Any]:
    copy = await client.record_test_data(application_id, note)
    return success({
        "dev_copy": copy,
        "note": (
            "Recorded with the dev copy. When it goes to production you will be reminded of it, and the approver "
            "sees it if the data is copied."
        ),
    })


async def reset_dev(client: PlatformClient, application_id: str) -> dict[str, Any]:
    removed = await client.reset_dev(application_id)
    return success({
        "application_id": application_id,
        "removed": removed.get("removed"),
        "note": "The dev copy, its code and its data are gone. The next dev deployment makes a new one, at a new address.",
    })
