"""
Zabbix Tool - Thin MCP wrapper for Zabbix monitoring API

This module provides access to Zabbix monitoring data.
All credentials are handled by the MCP Gateway.

Example usage:
    from zabbix import get_hosts, get_problems, get_history

    # Get all hosts
    hosts = get_hosts()
    for host in hosts:
        print(f"{host['host']}: {host['name']}")

    # Get current problems
    problems = get_problems(severity_min=3)  # Warning and above
    for p in problems:
        print(f"[{p['severity']}] {p['name']}")

    # Get metric history
    history = get_history(itemids=["12345"], limit=10)
"""

import sys
import os

# Add parent directory to path for mcp_client import
# Use realpath to resolve symlinks (tools are symlinked from skills/*/scripts/)
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.realpath(__file__))))

from mcp_client import call


def get_hosts(output: str = "extend", filter: dict = None, search: dict = None,
              start_search: bool = True, limit: int = None) -> list:
    """
    Get hosts from Zabbix monitoring system.

    Args:
        output: Output format ("extend" or list of fields). The server defaults
            to [hostid, host, name, status, available] when omitted, which is
            optimal for most investigations. Pass "extend" only if you need
            every field.
        filter: Exact-match filter conditions (e.g., {"host": ["server1", "server2"]}).
            Prefer filter over search when exact hostnames are known — it uses
            indexed lookups and is much faster on large Zabbix installations.
        search: Substring/prefix search conditions (e.g., {"name": "web"}).
            Combined with start_search=True (default), this performs a prefix
            match which is faster than a full substring scan.
        start_search: When True (default), search matches from the beginning
            of fields only (prefix match). Set to False for full substring
            matching. Prefix matching is significantly faster on large databases.
        limit: Maximum number of hosts to return

    Returns:
        List of host dictionaries with fields like:
            - hostid: Host ID
            - host: Technical host name
            - name: Visible host name
            - status: 0=enabled, 1=disabled
            - available: 0=unknown, 1=available, 2=unavailable

    Example:
        # Get all hosts (server selects optimal fields)
        hosts = get_hosts()

        # Search for hosts starting with "web"
        hosts = get_hosts(search={"name": "web"})

        # Filter by exact hostnames (faster than search)
        hosts = get_hosts(filter={"host": ["webserver1", "webserver2"]})
    """
    args = {"output": output, "start_search": start_search}
    if filter:
        args["filter"] = filter
    if search:
        args["search"] = search
    if limit:
        args["limit"] = limit
    return call("zabbix.get_hosts", args)


def get_problems(recent: bool = True, severity_min: int = 0, hostids: list = None, limit: int = None) -> list:
    """
    Get current problems/alerts from Zabbix.

    Args:
        recent: Only return recent problems (default: True)
        severity_min: Minimum severity level (0-5, where 5 is disaster)
            0: Not classified
            1: Information
            2: Warning
            3: Average
            4: High
            5: Disaster
        hostids: Filter by host IDs
        limit: Maximum number of problems to return

    Returns:
        List of problem dictionaries with fields like:
            - eventid: Event ID
            - source: Source type
            - object: Object type
            - objectid: Related object ID
            - name: Problem name
            - severity: Severity level
            - acknowledged: Whether problem is acknowledged
            - hosts: List of affected hosts (hostid, host, name)

    Example:
        # Get all current problems
        problems = get_problems()

        # Get only high severity and above
        problems = get_problems(severity_min=4)

        # Get problems for specific hosts
        problems = get_problems(hostids=["10084", "10085"])
    """
    args = {"recent": recent, "severity_min": severity_min}
    if hostids:
        args["hostids"] = hostids
    if limit:
        args["limit"] = limit
    return call("zabbix.get_problems", args)


def get_history(itemids: list, history: int = 0, time_from: int = None, time_till: int = None,
                limit: int = None, sortfield: str = "clock", sortorder: str = "DESC") -> list:
    """
    Get metric history data from Zabbix.

    Args:
        itemids: List of item IDs to get history for
        history: History type:
            0: Numeric (float)
            1: Character
            2: Log
            3: Numeric (unsigned)
            4: Text
        time_from: Start timestamp (Unix epoch)
        time_till: End timestamp (Unix epoch)
        limit: Maximum number of records to return
        sortfield: Field to sort by (default: "clock")
        sortorder: Sort order ("ASC" or "DESC", default: "DESC")

    Returns:
        List of history records with fields like:
            - itemid: Item ID
            - clock: Timestamp
            - value: Metric value
            - ns: Nanoseconds

    Example:
        import time

        # Get last 100 values for an item
        history = get_history(itemids=["12345"], limit=100)

        # Get values from last hour
        now = int(time.time())
        history = get_history(
            itemids=["12345"],
            time_from=now - 3600,
            time_till=now
        )
    """
    args = {
        "itemids": itemids,
        "history": history,
        "sortfield": sortfield,
        "sortorder": sortorder
    }
    if time_from:
        args["time_from"] = time_from
    if time_till:
        args["time_till"] = time_till
    if limit:
        args["limit"] = limit
    return call("zabbix.get_history", args)


def get_items(hostids: list = None, filter: dict = None, search: dict = None,
              start_search: bool = True, output: str = "extend", limit: int = None) -> list:
    """
    Get items (metrics) from Zabbix.

    Args:
        hostids: Filter by host IDs
        filter: Exact-match filter (e.g., {"key_": "system.cpu.util"}).
            Prefer filter over search when exact item keys are known — it uses
            indexed lookups and is much faster.
        search: Substring/prefix search conditions (e.g., {"key_": "cpu"}).
            Combined with start_search=True (default), this performs a prefix
            match which is faster than a full substring scan.
        start_search: When True (default), search matches from the beginning
            of fields only (prefix match). Set to False for full substring
            matching. Prefix matching is significantly faster on large databases.
        output: Output format. The server defaults to [itemid, hostid, name,
            key_, value_type, lastvalue, units, state, status] when omitted,
            which covers most investigation needs.
        limit: Maximum number of items to return

    Returns:
        List of item dictionaries with fields like:
            - itemid: Item ID
            - hostid: Host ID
            - name: Item name
            - key_: Item key
            - value_type: Value type
            - lastvalue: Last value
            - units: Value units

    Example:
        # Get all items for a host (server selects optimal fields)
        items = get_items(hostids=["10084"])

        # Search for CPU-related items (prefix match)
        items = get_items(search={"key_": "system.cpu"})

        # Exact key match (faster than search)
        items = get_items(filter={"key_": "system.cpu.util"}, hostids=["10084"])
    """
    args = {"output": output, "start_search": start_search}
    if hostids:
        args["hostids"] = hostids
    if filter:
        args["filter"] = filter
    if search:
        args["search"] = search
    if limit:
        args["limit"] = limit
    return call("zabbix.get_items", args)


def get_triggers(hostids: list = None, only_true: bool = False, min_severity: int = 0,
                 output: str = "extend") -> list:
    """
    Get triggers from Zabbix.

    Args:
        hostids: Filter by host IDs
        only_true: Return only triggers in problem state
        min_severity: Minimum severity level (0-5)
        output: Output format. The server defaults to [triggerid, description,
            priority, status, value, state] when omitted.

    Returns:
        List of trigger dictionaries with fields like:
            - triggerid: Trigger ID
            - description: Trigger description
            - priority: Severity level
            - status: 0=enabled, 1=disabled
            - value: 0=OK, 1=problem
            - hosts: List of related hosts (hostid, host, name)

    Example:
        # Get all triggers in problem state
        triggers = get_triggers(only_true=True)

        # Get high severity triggers for a host
        triggers = get_triggers(hostids=["10084"], min_severity=4)
    """
    args = {
        "output": output,
        "only_true": only_true,
        "min_severity": min_severity
    }
    if hostids:
        args["hostids"] = hostids
    return call("zabbix.get_triggers", args)


def get_items_batch(searches: list, hostids: list = None, start_search: bool = True,
                    output: str = "extend", limit_per_search: int = 10) -> dict:
    """
    Get multiple items in a single request with deduplication.

    This function is more efficient than multiple get_items() calls when you need
    to search for items matching multiple patterns (e.g., cpu, memory, disk).
    Results are cached and deduplicated to reduce API load.

    Args:
        searches: List of search patterns to find items for (e.g., ["cpu", "memory", "disk"])
        hostids: Filter by host IDs
        start_search: When True (default), search matches from the beginning
            of key_ only (prefix match). Faster on large Zabbix databases.
        output: Output format. The server defaults to [itemid, hostid, name,
            key_, value_type, lastvalue, units] when omitted.
        limit_per_search: Maximum items per search pattern (default: 10)

    Returns:
        Dictionary with:
            - results: List of BatchResult objects, each containing:
                - pattern: The search pattern
                - items: List of matching items
                - count: Number of items found
            - total_items: Total unique items found
            - total_unique: Same as total_items (for clarity)
            - pattern_count: Number of search patterns processed

    Example:
        # Get CPU, memory, and disk items for investigation
        result = get_items_batch(
            searches=["cpu", "memory", "disk"],
            hostids=["10084"]
        )

        for r in result['results']:
            print(f"Pattern '{r['pattern']}': {r['count']} items")
            for item in r['items']:
                print(f"  - {item['name']}: {item['lastvalue']}")

        # More efficient than:
        # cpu_items = get_items(hostids=["10084"], search={"key_": "cpu"})
        # mem_items = get_items(hostids=["10084"], search={"key_": "memory"})
        # disk_items = get_items(hostids=["10084"], search={"key_": "disk"})
    """
    args = {
        "searches": searches,
        "output": output,
        "start_search": start_search,
        "limit_per_search": limit_per_search
    }
    if hostids:
        args["hostids"] = hostids
    return call("zabbix.get_items_batch", args)


def api_request(method: str, params: dict = None) -> any:
    """
    Make a raw Zabbix API request.

    This function allows calling any Zabbix API method directly.

    Args:
        method: Zabbix API method (e.g., "host.get", "item.get")
        params: Parameters for the API method

    Returns:
        API response result

    Example:
        # Get host groups
        groups = api_request("hostgroup.get", {"output": "extend"})

        # Create a maintenance window
        result = api_request("maintenance.create", {
            "name": "Test maintenance",
            "active_since": 1609459200,
            "active_till": 1609462800,
            "hostids": ["10084"]
        })
    """
    args = {"method": method}
    if params:
        args["params"] = params
    return call("zabbix.api_request", args)
