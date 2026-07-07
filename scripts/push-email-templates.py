#!/usr/bin/env python3
"""Push the auth email templates in supabase/emails/ to the hosted Supabase
project via the Management API. Surgical PATCH: only the mailer fields and
site_url are touched, nothing else in the auth config.

Env: SUPABASE_ACCESS_TOKEN (sbp_..., account personal access token)
     SUPABASE_PROJECT_REF  (e.g. tofxluhjxeaorulptceu)
"""

import json
import os
import sys
import urllib.error
import urllib.request

EMAILS_DIR = os.path.join(os.path.dirname(__file__), "..", "supabase", "emails")
TEMPLATES = {
    "confirmation": "confirmation.html",
    "recovery": "recovery.html",
    "magic_link": "magic-link.html",
    "invite": "invite.html",
    "email_change": "email-change.html",
}


def main() -> int:
    token = os.environ.get("SUPABASE_ACCESS_TOKEN")
    ref = os.environ.get("SUPABASE_PROJECT_REF")
    if not token or not ref:
        print("SUPABASE_ACCESS_TOKEN and SUPABASE_PROJECT_REF are required")
        return 1

    with open(os.path.join(EMAILS_DIR, "config.json")) as f:
        config = json.load(f)

    payload: dict[str, str] = {"site_url": config["site_url"]}
    for key, filename in TEMPLATES.items():
        with open(os.path.join(EMAILS_DIR, filename)) as f:
            payload[f"mailer_templates_{key}_content"] = f.read()
        payload[f"mailer_subjects_{key}"] = config["subjects"][key]

    req = urllib.request.Request(
        f"https://api.supabase.com/v1/projects/{ref}/config/auth",
        data=json.dumps(payload).encode(),
        headers={
            "Authorization": f"Bearer {token}",
            "Content-Type": "application/json",
        },
        method="PATCH",
    )
    try:
        with urllib.request.urlopen(req) as resp:
            body = json.load(resp)
    except urllib.error.HTTPError as e:
        print(f"PATCH failed: {e.code} {e.read().decode()[:500]}")
        return 1

    # Echo back what the API now holds, as confirmation.
    print(f"site_url: {body.get('site_url')}")
    for key in TEMPLATES:
        subject = body.get(f"mailer_subjects_{key}")
        content = body.get(f"mailer_templates_{key}_content") or ""
        print(f"{key}: subject={subject!r} content={len(content)} bytes")
    return 0


if __name__ == "__main__":
    sys.exit(main())
