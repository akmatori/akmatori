"""
Zabbix Tool - Python wrapper for MCP Gateway Zabbix operations

All credentials are handled by the MCP Gateway.

Usage:
    from zabbix import get_hosts, get_problems, get_history, get_items_batch, acknowledge_event

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
    if search:
        args["search"] = search
    if filter:
        args["filter"] = filter
    if limit:
        args["limit"] = limit
    if tool_instance_id is not None:
        args["tool_instance_id"] = tool_instance_id
    return call("zabbix.get_hosts", args)


def get_problems(recent: bool = True, severity_min: int = 0,
                 tool_instance_id: int = None) -> list:
    """
    Get current problems/alerts from Zabbix.

    Args:
        recent: Only return recent problems (default: True)
        severity_min: Minimum severity level 0-5
        tool_instance_id: Optional tool instance ID for routing

    Returns:
        List of problem dictionaries
    """
    args = {"recent": recent, "severity_min": severity_min}
    if tool_instance_id is not None:
        args["tool_instance_id"] = tool_instance_id
    return call("zabbix.get_problems", args)


def get_history(itemids: list, history_type: int = 0, limit: int = None,
                tool_instance_id: int = None) -> list:
    """
    Get metric history data from Zabbix.

    Args:
        itemids: Item IDs to get history for
        history_type: 0=float, 1=string, 2=log, 3=uint, 4=text
        limit: Maximum number of records
        tool_instance_id: Optional tool instance ID for routing

    Returns:
        List of history records
    """
    args = {"itemids": itemids, "history_type": history_type}
    if limit:
        args["limit"] = limit
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


def acknowledge_event(eventids: list, message: str,
                      tool_instance_id: int = None) -> dict:
    """
    Acknowledge a Zabbix event/problem with a message.

    Args:
        eventids: Event IDs to acknowledge
        message: Acknowledgement message
        tool_instance_id: Optional tool instance ID for routing

    Returns:
        Acknowledgement result
    """
    args = {"eventids": eventids, "message": message}
    if tool_instance_id is not None:
        args["tool_instance_id"] = tool_instance_id
    return call("zabbix.acknowledge_event", args)
