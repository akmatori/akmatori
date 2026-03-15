"""
VictoriaMetrics Tool - Python wrapper for MCP Gateway VictoriaMetrics operations

All credentials are handled by the MCP Gateway.

Usage:
    from victoriametrics import instant_query, range_query, label_values, series, api_request

    result = instant_query("up", tool_instance_id=1)
    result = range_query("rate(http_requests_total[5m])", start="2h", end="now", step="1m", tool_instance_id=1)
    result = label_values("__name__", tool_instance_id=1)
    result = series(match="up", tool_instance_id=1)
    result = api_request("/api/v1/status/tsdb", tool_instance_id=1)
"""

import sys
import os

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.realpath(__file__))))

from mcp_client import call


def instant_query(query: str, eval_time: str = None, step: str = None,
                  timeout: str = None, tool_instance_id: int = None) -> dict:
    """
    Execute an instant PromQL query against VictoriaMetrics.

    Args:
        query: PromQL query string
        eval_time: Evaluation timestamp (RFC3339 or Unix timestamp)
        step: Query resolution step width
        timeout: Evaluation timeout
        tool_instance_id: Optional tool instance ID for routing

    Returns:
        Query result data
    """
    args = {"query": query}
    if eval_time is not None:
        args["time"] = eval_time
    if step is not None:
        args["step"] = step
    if timeout is not None:
        args["timeout"] = timeout
    if tool_instance_id is not None:
        args["tool_instance_id"] = tool_instance_id
    return call("victoriametrics.instant_query", args)


def range_query(query: str, start: str, end: str, step: str,
                timeout: str = None, tool_instance_id: int = None) -> dict:
    """
    Execute a range PromQL query against VictoriaMetrics.

    Args:
        query: PromQL query string
        start: Start timestamp (RFC3339 or Unix timestamp or relative like "2h")
        end: End timestamp (RFC3339 or Unix timestamp or "now")
        step: Query resolution step width (e.g. "1m", "5m")
        timeout: Evaluation timeout
        tool_instance_id: Optional tool instance ID for routing

    Returns:
        Query result data with time series
    """
    args = {"query": query, "start": start, "end": end, "step": step}
    if timeout is not None:
        args["timeout"] = timeout
    if tool_instance_id is not None:
        args["tool_instance_id"] = tool_instance_id
    return call("victoriametrics.range_query", args)


def label_values(label_name: str, match: str = None, start: str = None,
                 end: str = None, tool_instance_id: int = None) -> list:
    """
    Get label values from VictoriaMetrics.

    Args:
        label_name: Label name to get values for (e.g. "__name__", "job")
        match: Optional series selector to filter by (e.g. 'up{job="prometheus"}')
        start: Optional start timestamp
        end: Optional end timestamp
        tool_instance_id: Optional tool instance ID for routing

    Returns:
        List of label values
    """
    args = {"label_name": label_name}
    if match is not None:
        args["match"] = match
    if start is not None:
        args["start"] = start
    if end is not None:
        args["end"] = end
    if tool_instance_id is not None:
        args["tool_instance_id"] = tool_instance_id
    return call("victoriametrics.label_values", args)


def series(match: str, start: str = None, end: str = None,
           tool_instance_id: int = None) -> list:
    """
    Find series matching label selectors in VictoriaMetrics.

    Args:
        match: Series selector (e.g. "up" or "process_start_time_seconds{job='prometheus'}")
        start: Optional start timestamp
        end: Optional end timestamp
        tool_instance_id: Optional tool instance ID for routing

    Returns:
        List of matching series label sets
    """
    args = {"match": match}
    if start is not None:
        args["start"] = start
    if end is not None:
        args["end"] = end
    if tool_instance_id is not None:
        args["tool_instance_id"] = tool_instance_id
    return call("victoriametrics.series", args)


def api_request(path: str, method: str = "GET", params: dict = None,
                tool_instance_id: int = None) -> dict:
    """
    Make a generic VictoriaMetrics API request.

    Args:
        path: API path (e.g. "/api/v1/status/tsdb")
        method: HTTP method (GET or POST, default: GET)
        params: Optional query/form parameters
        tool_instance_id: Optional tool instance ID for routing

    Returns:
        API response data
    """
    args = {"path": path, "method": method}
    if params is not None:
        args["params"] = params
    if tool_instance_id is not None:
        args["tool_instance_id"] = tool_instance_id
    return call("victoriametrics.api_request", args)
