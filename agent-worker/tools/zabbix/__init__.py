"""
Zabbix Tool - Python wrapper for MCP Gateway Zabbix operations

All credentials are handled by the MCP Gateway.

Usage:
    from zabbix import get_hosts, get_problems, get_history, get_items, get_items_batch, get_triggers, api_request

    hosts = get_hosts(tool_instance_id=2)
    problems = get_problems(severity_min=3, tool_instance_id=2)
    items = get_items_batch(searches=["cpu", "memory"], tool_instance_id=2)
"""

import sys
import os

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.realpath(__file__))))

from mcp_client import call


def get_hosts(search: dict = None, filter: dict = None, limit: int = None,
              tool_instance_id: int = None) -> list:
    """
    Get hosts from Zabbix monitoring system.

    Args:
        search: Substring search (e.g. {"name": "web-server"})
        filter: Exact-match filter (e.g. {"host": ["server1", "server2"]})
        limit: Maximum number of hosts to return
        tool_instance_id: Optional tool instance ID for routing

    Returns:
        List of host dictionaries
    """
    args = {}
    if search is not None:
        args["search"] = search
    if filter is not None:
        args["filter"] = filter
    if limit is not None:
        args["limit"] = limit
    if tool_instance_id is not None:
        args["tool_instance_id"] = tool_instance_id
    return call("zabbix.get_hosts", args)


def get_problems(recent: bool = True, severity_min: int = 0,
                 hostids: list = None, limit: int = None,
                 tool_instance_id: int = None) -> list:
    """
    Get current problems/alerts from Zabbix.

    Args:
        recent: Only return recent problems (default: True)
        severity_min: Minimum severity level 0-5
        hostids: Optional list of host IDs to filter by
        limit: Maximum number of problems to return
        tool_instance_id: Optional tool instance ID for routing

    Returns:
        List of problem dictionaries
    """
    args = {"recent": recent, "severity_min": severity_min}
    if hostids is not None:
        args["hostids"] = hostids
    if limit is not None:
        args["limit"] = limit
    if tool_instance_id is not None:
        args["tool_instance_id"] = tool_instance_id
    return call("zabbix.get_problems", args)


def get_history(itemids: list, history_type: int = 0, limit: int = None,
                time_from: int = None, time_till: int = None,
                sortfield: str = None, sortorder: str = None,
                tool_instance_id: int = None) -> list:
    """
    Get metric history data from Zabbix.

    Args:
        itemids: Item IDs to get history for
        history_type: 0=float, 1=string, 2=log, 3=uint, 4=text
        limit: Maximum number of records
        time_from: Start timestamp (Unix epoch)
        time_till: End timestamp (Unix epoch)
        sortfield: Field to sort by (default: clock)
        sortorder: Sort order: ASC or DESC (default: DESC)
        tool_instance_id: Optional tool instance ID for routing

    Returns:
        List of history records
    """
    args = {"itemids": itemids, "history": history_type}
    if limit is not None:
        args["limit"] = limit
    if time_from is not None:
        args["time_from"] = time_from
    if time_till is not None:
        args["time_till"] = time_till
    if sortfield is not None:
        args["sortfield"] = sortfield
    if sortorder is not None:
        args["sortorder"] = sortorder
    if tool_instance_id is not None:
        args["tool_instance_id"] = tool_instance_id
    return call("zabbix.get_history", args)


def get_items_batch(searches: list, hostids: list = None,
                    tool_instance_id: int = None) -> list:
    """
    Get multiple items (metrics) from Zabbix in a single efficient request.

    Args:
        searches: Search patterns (e.g. ["cpu", "memory", "disk"])
        hostids: Optional host IDs to filter by
        tool_instance_id: Optional tool instance ID for routing

    Returns:
        List of item dictionaries
    """
    args = {"searches": searches}
    if hostids:
        args["hostids"] = hostids
    if tool_instance_id is not None:
        args["tool_instance_id"] = tool_instance_id
    return call("zabbix.get_items_batch", args)


def get_items(hostids: list = None, search: dict = None, filter: dict = None,
              limit: int = None, tool_instance_id: int = None) -> list:
    """
    Get items (metrics) from Zabbix.

    Args:
        hostids: Optional list of host IDs to filter by
        search: Substring search (e.g. {"key_": "cpu"})
        filter: Exact-match filter (e.g. {"key_": "system.cpu.util"})
        limit: Maximum number of items to return
        tool_instance_id: Optional tool instance ID for routing

    Returns:
        List of item dictionaries
    """
    args = {}
    if hostids is not None:
        args["hostids"] = hostids
    if search is not None:
        args["search"] = search
    if filter is not None:
        args["filter"] = filter
    if limit is not None:
        args["limit"] = limit
    if tool_instance_id is not None:
        args["tool_instance_id"] = tool_instance_id
    return call("zabbix.get_items", args)


def get_triggers(hostids: list = None, only_true: bool = False,
                 min_severity: int = 0, tool_instance_id: int = None) -> list:
    """
    Get triggers from Zabbix.

    Args:
        hostids: Optional list of host IDs to filter by
        only_true: Return only triggers in problem state (default: False)
        min_severity: Minimum severity level 0-5 (default: 0)
        tool_instance_id: Optional tool instance ID for routing

    Returns:
        List of trigger dictionaries
    """
    args = {}
    if hostids is not None:
        args["hostids"] = hostids
    if only_true:
        args["only_true"] = only_true
    if min_severity > 0:
        args["min_severity"] = min_severity
    if tool_instance_id is not None:
        args["tool_instance_id"] = tool_instance_id
    return call("zabbix.get_triggers", args)


def api_request(method: str, params: dict = None,
                tool_instance_id: int = None) -> dict:
    """
    Make a raw Zabbix API request.

    Args:
        method: Zabbix API method (e.g. 'host.get', 'item.get')
        params: Parameters for the API method
        tool_instance_id: Optional tool instance ID for routing

    Returns:
        API response
    """
    args = {"method": method}
    if params is not None:
        args["params"] = params
    if tool_instance_id is not None:
        args["tool_instance_id"] = tool_instance_id
    return call("zabbix.api_request", args)
