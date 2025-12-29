"""
Utility functions for direct Zabbix API calls
"""

import json
import ssl
import urllib.error
import urllib.request
from typing import Any, Dict

from .config import (
    get_auth,
    get_timeout,
    get_verify_ssl,
    get_zabbix_api_url,
    debug_log,
)


# Counter for generating unique request IDs
_request_id = 1


def zabbix_request(method: str, params: Any = None) -> str:
    """
    Make a direct request to the Zabbix API

    Args:
        method: Zabbix API method (e.g., 'host.get', 'item.create')
        params: Parameters to pass to the method

    Returns:
        JSON string response from Zabbix

    Raises:
        Exception: If the request fails

    Example:
        result = zabbix_request('host.get', {'output': 'extend'})
        hosts = json.loads(result)
    """
    global _request_id

    if params is None:
        params = {}

    url = get_zabbix_api_url()
    auth = get_auth()

    # Build the JSON-RPC request body
    request_body: Dict[str, Any] = {
        'jsonrpc': '2.0',
        'method': method,
        'params': params,
        'id': _request_id
    }

    _request_id += 1

    # Add auth token if using username/password authentication
    if auth:
        request_body['auth'] = auth

    debug_log(f'Calling {method}', params)

    payload = json.dumps(request_body).encode("utf-8")
    headers = {
        "Content-Type": "application/json-rpc",
    }
    request = urllib.request.Request(url, data=payload, headers=headers, method="POST")
    timeout = get_timeout()
    context = None
    if url.lower().startswith("https"):
        if get_verify_ssl():
            context = ssl.create_default_context()
        else:
            context = ssl.create_default_context()
            context.check_hostname = False
            context.verify_mode = ssl.CERT_NONE

    try:
        with urllib.request.urlopen(request, timeout=timeout, context=context) as response:
            resp_data = response.read()
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="ignore")
        debug_log(f"{method} failed:", f"{exc.code} {body}")
        raise Exception(f"Zabbix API request failed ({exc.code}): {body or exc.reason}")
    except urllib.error.URLError as exc:
        debug_log(f"{method} failed:", str(exc))
        raise Exception(f"Failed to call {method}: {str(exc)}")
    except Exception as exc:
        debug_log(f"{method} failed:", str(exc))
        raise Exception(f"Failed to call {method}: {str(exc)}")

    try:
        result = json.loads(resp_data)
    except json.JSONDecodeError as exc:
        raise Exception(f"Invalid JSON response from {method}: {exc}")

    if "error" in result:
        error = result["error"]
        debug_log(f"{method} failed:", error)
        raise Exception(
            f"Zabbix API error: {error.get('message', 'Unknown error')} "
            f"(code: {error.get('code', 'unknown')}, data: {error.get('data', 'none')})"
        )

    debug_log(f"{method} completed successfully")
    return json.dumps(result["result"])
