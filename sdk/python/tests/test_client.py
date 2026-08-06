from __future__ import annotations

import json
import threading
import unittest
from io import BytesIO
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib import parse

from relay_sdk import (
    RelayBrokerNotReadyError,
    RelayCancelRejectedError,
    RelayClient,
    RelayCommandOutcomeUnknownError,
    RelayIdempotencyError,
    RelayQueryInterruptedError,
)
from relay_sdk.client import _fill_key, _order_key
from relay_sdk.errors import error_from_payload
from relay_sdk.models import Fill, Order
from relay_sdk.streaming import iter_sse_events


class RelayHandler(BaseHTTPRequestHandler):
    requests = []

    def do_GET(self):  # noqa: N802
        parsed = parse.urlparse(self.path)
        query = parse.parse_qs(parsed.query)
        RelayHandler.requests.append(("GET", parsed.path, query, None))
        if parsed.path == "/v1/status":
            self._json(
                {
                    "ok": True,
                    "data": {
                        "service": "relay-api",
                        "status": "ok",
                        "dependencies": {"database": {"status": "ok"}},
                    },
                }
            )
            return
        if parsed.path == "/v1/accounts":
            self._json({"ok": True, "data": {"accounts": [{"account_id": "acct-1", "enabled": True}]}})
            return
        if parsed.path == "/v1/accounts/acct-1/asset":
            self._json({"ok": True, "data": {"asset": {"account_id": "acct-1", "net_asset": 123.45}}})
            return
        if parsed.path == "/v1/accounts/acct-1/positions":
            self._json({"ok": True, "data": {"positions": [{"account_id": "acct-1", "symbol": "600000", "quantity": 100, "avg_cost": 9.54, "total_cost": 954.0, "avg_cost_source": "broker_total_position_cost", "cost_complete": True}]}})
            return
        if parsed.path == "/v1/accounts/acct-1/positions/history":
            self._json({"ok": True, "data": {"positions": [{"account_id": "acct-1", "trade_date": "2026-06-12", "snapshot_type": query.get("snapshot_type", ["close"])[0], "symbol": "600000", "quantity": 100}]}})
            return
        if parsed.path == "/v1/accounts/acct-1/performance/daily":
            self._json({"ok": True, "data": {"account_id": "acct-1", "trade_date": query.get("trade_date", [""])[0], "net_asset": 123.45}})
            return
        if parsed.path == "/v1/accounts/acct-1/performance/contributions":
            self._json(
                {
                    "ok": True,
                    "data": {
                        "contribution": {
                            "account_id": "acct-1",
                            "trade_date": query.get("trade_date", ["20260612"])[0],
                            "contributions": [
                                {
                                    "security_id": "600000.SH",
                                    "strategy_type": "stock_cross_section",
                                    "net_contribution": 100.0,
                                }
                            ],
                        }
                    },
                }
            )
            return
        if parsed.path == "/v1/accounts/acct-1/performance/trade-quality":
            self._json(
                {
                    "ok": True,
                    "data": {
                        "trade_quality": {
                            "account_id": "acct-1",
                            "date_from": query.get("date_from", query.get("trade_date", ["20260612"]))[0],
                            "date_to": query.get("date_to", query.get("trade_date", ["20260612"]))[0],
                            "summary": {
                                "orders": 10,
                                "orders_with_fills": 8,
                                "executed_order_rate": 0.8,
                            },
                            "anomalies": [],
                        }
                    },
                }
            )
            return
        if parsed.path == "/v1/accounts/acct-1/performance/series":
            self._json({"ok": True, "data": {"account_id": "acct-1", "series": [{"trade_date": "20260612", "net_asset": 123.45}]}})
            return
        if parsed.path == "/v1/accounts/acct-1/performance/economic-nav/preview":
            self._json(
                {
                    "ok": True,
                    "data": {
                        "economic_nav": {
                            "account_id": "acct-1",
                            "trade_date": query.get("trade_date", [""])[0],
                            "persisted": False,
                            "nav": {"status": query.get("status", ["provisional"])[0]},
                        }
                    },
                }
            )
            return
        if parsed.path == "/v1/accounts/acct-1/performance/cost-ledger/preview":
            self._json(
                {
                    "ok": True,
                    "data": {
                        "cost_ledger": {
                            "account_id": "acct-1",
                            "trade_date": query.get("trade_date", [""])[0],
                            "status": "calculated",
                            "persisted": False,
                            "quality_flags": [],
                        }
                    },
                }
            )
            return
        if parsed.path == "/v1/accounts/acct-1/performance/economic-nav/reconcile":
            self._json(
                {
                    "ok": True,
                    "data": {
                        "economic_nav_reconciliation": {
                            "account_id": "acct-1",
                            "trade_date": query.get("trade_date", [""])[0],
                            "observed_trade_date": query.get("observed_trade_date", [""])[0],
                            "persisted": False,
                            "status": "auto_completed",
                        }
                    },
                }
            )
            return
        if parsed.path == "/v1/accounts/acct-1/performance/economic-nav":
            self._json({"ok": True, "data": {"navs": [{"account_id": "acct-1", "trade_date": query.get("trade_date", [""])[0], "status": "provisional"}]}})
            return
        if parsed.path == "/v1/accounts/acct-1/performance/nav-reconciliations":
            self._json({"ok": True, "data": {"reconciliations": [{"account_id": "acct-1", "trade_date": query.get("trade_date", [""])[0], "status": "auto_completed"}]}})
            return
        if parsed.path == "/v1/accounts/acct-1/performance/series.csv":
            body = b"account_id,trade_date,net_asset\nacct-1,20260612,123.45\n"
            self.send_response(200)
            self.send_header("Content-Type", "text/csv")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        if parsed.path == "/v1/reconciliations/breaks":
            self._json({"ok": True, "data": {"breaks": [{"run_id": query.get("run_id", ["run-1"])[0], "status": query.get("status", ["open"])[0]}]}})
            return
        if parsed.path == "/v1/reconciliations/review-report":
            self._json({"ok": True, "data": {"trade_date": query.get("trade_date", ["20260731"])[0], "status": "passed", "accounts": []}})
            return
        if parsed.path == "/v1/jobs/runs":
            self._json({"ok": True, "data": {"runs": [{"job_name": "post_close_settlement", "target_trade_date": query.get("trade_date", [""])[0]}]}})
            return
        if parsed.path == "/v1/query-status/msg-asset-1":
            self._json(
                {
                    "ok": True,
                    "data": {
                        "origin_message_id": "msg-asset-1",
                        "account_id": "acct-1",
                        "action": "account.asset.query",
                        "expected_result_type": "asset_page",
                        "state": "completed",
                        "terminal": True,
                        "success": True,
                        "contradictory": False,
                        "reply_count": 1,
                        "terminal_count": 1,
                        "replies": [{"status": "completed", "result_type": "asset_page", "is_last": True}],
                    },
                }
            )
            return
        if parsed.path == "/v1/meridian/market/bars":
            self._json(
                {
                    "ok": True,
                    "data": {
                        "data": [
                            {
                                "security_id": query.get("security_id", ["600000.SH"])[0],
                                "trade_date": 20260612,
                                "datetime": "2026-06-12T09:31:00+08:00",
                                "close": 9.46,
                            }
                        ],
                        "meta": {"schema_version": "market_bar.v1"},
                    },
                }
            )
            return
        if parsed.path == "/v1/meridian/metadata/adjust-factors":
            self._json(
                {
                    "ok": True,
                    "data": {
                        "data": [
                            {
                                "security_id": query.get("security_id", ["600000.SH"])[0],
                                "trade_date": 20260612,
                                "adj_factor": 1.2345,
                            }
                        ],
                        "meta": {"schema_version": "metadata_adjust_factor.v1"},
                    },
                }
            )
            return
        if parsed.path == "/v1/meridian/market/etf-components":
            self._json(
                {
                    "ok": True,
                    "data": {
                        "data": [
                            {
                                "security_id": query.get("security_id", ["588200.SH"])[0],
                                "component_security_id": "688361.SH",
                                "stock_amount": "425",
                            }
                        ],
                        "meta": {"schema_version": "etf_component.v1"},
                    },
                }
            )
            return
        if parsed.path == "/v1/meridian/market/etf-cash-components":
            self._json(
                {
                    "ok": True,
                    "data": {
                        "data": [
                            {
                                "security_id": query.get("security_id", ["588200.SH"])[0],
                                "unit_subscribe_redeem": "4500000",
                            }
                        ],
                        "meta": {"schema_version": "etf_cash_component.v1"},
                    },
                }
            )
            return
        if parsed.path == "/v1/meridian/market/etf-pcf-status":
            self._json(
                {
                    "ok": True,
                    "data": {
                        "data": {"schema_version": "etf_pcf_status.v1", "state": {"status": "success"}},
                        "meta": {"schema_version": "etf_pcf_status.v1"},
                    },
                }
            )
            return
        if parsed.path == "/v1/orders":
            self._json(
                {
                    "ok": True,
                    "data": {
                        "orders": [
                            {
                                "account_id": "acct-1",
                                "gateway_order_id": query.get("gateway_order_id", ["gw-1"])[0],
                                "status": "filled",
                                "is_terminal": True,
                                "cum_filled_qty": 100,
                            }
                        ]
                    },
                }
            )
            return
        if parsed.path == "/v1/fills":
            if query.get("symbol", [""])[0] == "dup-fill":
                self._json(
                    {
                        "ok": True,
                        "data": {
                            "fills": [
                                {
                                    "fill_id": "reused-fill",
                                    "account_id": "acct-1",
                                    "gateway_order_id": "gw-a",
                                    "qty": 100,
                                },
                                {
                                    "fill_id": "reused-fill",
                                    "account_id": "acct-1",
                                    "gateway_order_id": "gw-b",
                                    "qty": 200,
                                },
                            ]
                        },
                    }
                )
                return
            self._json({"ok": True, "data": {"fills": [{"fill_id": "fill-1", "account_id": "acct-1", "qty": 100}]}})
            return
        if parsed.path == "/v1/accounts/acct-1/fees":
            self._json(
                {
                    "ok": True,
                    "data": {
                        "fees": [{
                            "account_id": "acct-1",
                            "fee_record_id": "fee-1",
                            "trade_date": query.get("trade_date", [""])[0],
                            "gateway_order_id": query.get("gateway_order_id", ["gw-1"])[0],
                            "total_fee": 6.25,
                            "fee_complete": True,
                            "fee_source": "broker_order_fund_detail",
                            "association_complete": True,
                        }]
                    },
                }
            )
            return
        if parsed.path == "/v1/history/orders":
            self._json({"ok": True, "data": {"orders": [{"account_id": "acct-1", "gateway_order_id": "gw-history", "status": "filled", "is_terminal": True}]}})
            return
        if parsed.path == "/v1/history/fills":
            self._json({"ok": True, "data": {"fills": [{"fill_id": "fill-history", "account_id": "acct-1", "trade_date": query.get("trade_date", [""])[0], "qty": 100}]}})
            return
        if parsed.path in ("/v1/transfers", "/v1/history/transfers"):
            self._json(
                {
                    "ok": True,
                    "data": {
                        "transfers": [
                            {
                                "fill_id": "transfer-1",
                                "account_id": "acct-1",
                                "gateway_order_id": "gw-transfer",
                                "symbol": "300001",
                                "exchange": "SZ",
                                "qty": 300,
                                "trade_side": "R",
                                "business_type": "E",
                                "record_type": "etf_component_transfer",
                                "component_symbol": "300001",
                                "component_exchange": "SZ",
                                "component_qty": 300,
                                "component_value": None,
                            }
                        ]
                    },
                }
            )
            return
        if parsed.path == "/v1/events/stream":
            events = [
                (
                    "order.changed",
                    {
                        "type": "order.changed",
                        "account_ids": ["acct-1"],
                        "time": "2026-06-14T00:00:00Z",
                        "data": {"orders": 1, "last_stream_id": "1-0"},
                    },
                ),
                (
                    "order.cancel.rejected",
                    {
                        "type": "order.cancel.rejected",
                        "account_ids": ["acct-1"],
                        "time": "2026-07-30T02:00:00Z",
                        "data": {
                            "cancel_failures": 1,
                            "cancel_attempt": {
                                "gateway_order_id": "gw-cancel-rejected",
                                "status": "rejected",
                                "code": "BROKER_CANCEL_REJECTED",
                            },
                        },
                    },
                ),
                (
                    "fill.changed",
                    {
                        "type": "fill.changed",
                        "account_ids": ["acct-1"],
                        "time": "2026-06-14T00:00:01Z",
                        "data": {"fills": 1, "last_stream_id": "2-0"},
                    },
                ),
                (
                    "order.changed",
                    {
                        "type": "order.changed",
                        "account_ids": ["acct-1"],
                        "time": "2026-06-14T00:00:02Z",
                        "data": {"orders": 1, "last_stream_id": "3-0"},
                    },
                ),
            ]
            body = b"".join(
                (
                    f"event: {event_name}\n"
                    f"data: {json.dumps(payload, separators=(',', ':'))}\n"
                    "\n"
                ).encode("utf-8")
                for event_name, payload in events
            )
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        self.send_error(404)

    def do_POST(self):  # noqa: N802
        parsed = parse.urlparse(self.path)
        query = parse.parse_qs(parsed.query)
        length = int(self.headers.get("Content-Length", "0"))
        body = json.loads(self.rfile.read(length).decode("utf-8") or "{}")
        RelayHandler.requests.append(("POST", parsed.path, query, body))
        if parsed.path == "/v1/orders":
            if body.get("gateway_order_id") == "gw-replay":
                self._json(
                    {
                        "ok": True,
                        "data": {
                            "order": {
                                "account_id": body["account_id"],
                                "gateway_order_id": "gw-replay",
                                "client_order_id": body["client_order_id"],
                                "status": "cancelled",
                                "is_terminal": True,
                            },
                            "idempotency_key": body["idempotency_key"],
                            "replayed": True,
                        },
                    }
                )
                return
            order = {
                "account_id": body["account_id"],
                "gateway_order_id": body["gateway_order_id"],
                "client_order_id": body["client_order_id"],
                "status": "created",
            }
            self._json({"ok": True, "data": {"order": order, "stream_id": "1-0", "message_id": "msg-1"}}, status=202)
            return
        if parsed.path == "/v1/orders/gw-1/cancel":
            self._json(
                {
                    "ok": True,
                    "data": {
                        "order": {"account_id": body["account_id"], "gateway_order_id": "gw-1", "status": "working"},
                        "cancel_id": body["cancel_id"],
                    },
                },
                status=202,
            )
            return
        if parsed.path == "/v1/accounts/acct-1/orders/refresh":
            self._json({"ok": True, "data": {"account_id": "acct-1", "action": "order.list.query", "stream_id": "2-0"}}, status=202)
            return
        if parsed.path == "/v1/accounts/acct-1/fills/refresh":
            self._json({"ok": True, "data": {"account_id": "acct-1", "action": "fill.list.query", "stream_id": "3-0"}}, status=202)
            return
        if parsed.path == "/v1/accounts/acct-1/fees/refresh":
            self._json({"ok": True, "data": {"account_id": "acct-1", "action": "fee.list.query", "stream_id": "6-0"}}, status=202)
            return
        if parsed.path == "/v1/accounts/acct-1/asset/refresh" or parsed.path == "/v1/accounts/acct-1/positions/refresh":
            self._json({"ok": True, "data": {"account_id": "acct-1", "action": "query", "stream_id": "4-0"}}, status=202)
            return
        if parsed.path == "/v1/orders/batch":
            self._json({"ok": True, "data": {"orders": body["orders"], "stream_id": "5-0"}}, status=202)
            return
        if parsed.path == "/v1/jobs/runs":
            self._json({"ok": True, "data": {"run": {"run_id": "job-1", "job_name": body.get("job_name") or body.get("report", {}).get("job")}}}, status=202)
            return
        if parsed.path == "/v1/settlements/snapshots":
            self._json(
                {
                    "ok": True,
                    "data": {
                        "run_id": body.get("run_id"),
                        "trade_date": body.get("trade_date"),
                        "status": "completed",
                        "asset_snapshots": 1,
                        "position_snapshots": 1,
                    },
                },
                status=202,
            )
            return
        if parsed.path == "/v1/accounts/acct-1/performance/economic-nav/rebuild":
            self._json(
                {
                    "ok": True,
                    "data": {
                        "economic_nav": {
                            "account_id": "acct-1",
                            "trade_date": body.get("trade_date"),
                            "persisted": True,
                            "nav": {"status": body.get("status")},
                        }
                    },
                }
            )
            return
        if parsed.path == "/v1/accounts/acct-1/performance/cost-ledger/rebuild":
            self._json(
                {
                    "ok": True,
                    "data": {
                        "cost_ledger": {
                            "account_id": "acct-1",
                            "trade_date": query.get("trade_date", [""])[0],
                            "status": "calculated",
                            "persisted": True,
                            "quality_flags": [],
                        }
                    },
                }
            )
            return
        if parsed.path == "/v1/accounts/acct-1/performance/economic-nav/reconcile":
            self._json(
                {
                    "ok": True,
                    "data": {
                        "economic_nav_reconciliation": {
                            "account_id": "acct-1",
                            "trade_date": body.get("trade_date"),
                            "observed_trade_date": body.get("observed_trade_date"),
                            "persisted": True,
                            "status": "review_required",
                        }
                    },
                }
            )
            return
        if parsed.path == "/v1/accounts/acct-1/performance/nav-reconciliations/confirm":
            self._json(
                {
                    "ok": True,
                    "data": {
                        "nav_reconciliation_review": {
                            "account_id": "acct-1",
                            "trade_date": body.get("trade_date"),
                            "action": "confirm",
                            "status": "confirmed",
                            "persisted": True,
                        }
                    },
                }
            )
            return
        if parsed.path == "/v1/accounts/acct-1/performance/nav-reconciliations/block":
            self._json(
                {
                    "ok": True,
                    "data": {
                        "nav_reconciliation_review": {
                            "account_id": "acct-1",
                            "trade_date": body.get("trade_date"),
                            "action": "block",
                            "status": "blocked",
                            "persisted": True,
                        }
                    },
                }
            )
            return
        if parsed.path == "/v1/error":
            self._json({"ok": False, "error": {"code": "IDEMPOTENCY_CONFLICT", "message": "duplicate"}}, status=409)
            return
        if parsed.path == "/v1/broker-not-ready":
            self._json(
                {"ok": False, "error": {"code": "BROKER_NOT_READY", "message": "broker counter is reconnecting"}},
                status=503,
            )
            return
        self.send_error(404)

    def log_message(self, *_args):
        return

    def _json(self, payload, status=200):
        body = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


class RelayClientTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        RelayHandler.requests = []
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), RelayHandler)
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        cls.base_url = f"http://127.0.0.1:{cls.server.server_address[1]}"

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.thread.join(timeout=2)

    def setUp(self):
        RelayHandler.requests = []
        self.client = RelayClient(self.base_url, account_id="acct-1")

    def test_queries_return_models(self):
        self.assertEqual(self.client.status()["status"], "ok")
        self.assertEqual(self.client.list_accounts()[0].account_id, "acct-1")
        self.assertEqual(self.client.get_asset().net_asset, 123.45)
        position = self.client.get_positions()[0]
        self.assertEqual(position.symbol, "600000")
        self.assertEqual(position.total_cost, 954.0)
        self.assertEqual(position.avg_cost_source, "broker_total_position_cost")
        self.assertTrue(position.cost_complete)
        self.assertEqual(self.client.list_orders(gateway_order_id="gw-1")[0].status, "filled")
        self.assertEqual(self.client.list_fills()[0].fill_id, "fill-1")
        self.assertEqual(self.client.list_transfers()[0].component_qty, 300)

    def test_query_status_returns_terminal_model(self):
        status = self.client.get_query_status("msg-asset-1")
        self.assertEqual(status.state, "completed")
        self.assertTrue(status.success)
        self.assertEqual(status.expected_result_type, "asset_page")
        self.assertEqual(status.replies[0].result_type, "asset_page")

    def test_history_queries_use_history_endpoints(self):
        open_position = self.client.get_positions(history=True, trade_date="20260612", snapshot_type="open")[0]
        self.assertEqual(open_position.trade_date, "2026-06-12")
        self.assertEqual(open_position.snapshot_type, "open")
        self.assertEqual(self.client.list_orders(history=True, date_from="20260612")[0].gateway_order_id, "gw-history")
        self.assertEqual(self.client.list_fills(history=True, trade_date="20260612")[0].fill_id, "fill-history")
        self.assertEqual(self.client.list_transfers(history=True, trade_date="20260612")[0].fill_id, "transfer-1")
        self.assertEqual(RelayHandler.requests[-4][1], "/v1/accounts/acct-1/positions/history")
        self.assertEqual(RelayHandler.requests[-4][2]["snapshot_type"], ["open"])
        self.assertEqual(RelayHandler.requests[-3][1], "/v1/history/orders")
        self.assertEqual(RelayHandler.requests[-2][1], "/v1/history/fills")
        self.assertEqual(RelayHandler.requests[-1][1], "/v1/history/transfers")

    def test_record_job_run(self):
        run = self.client.record_job_run(
            {"ok": True, "job": "pre_open_init", "trading_day": {"target_trade_date": "20260614"}},
            trigger="unit",
            status="completed",
            target_trade_date="20260614",
            timezone="Asia/Shanghai",
            duration_ms=1200,
        )
        self.assertEqual(run["run_id"], "job-1")
        method, path, _query, body = RelayHandler.requests[-1]
        self.assertEqual((method, path), ("POST", "/v1/jobs/runs"))
        self.assertEqual(body["trigger"], "unit")
        self.assertEqual(body["status"], "succeeded")
        self.assertEqual(body["target_trade_date"], "20260614")
        self.assertEqual(body["timezone"], "Asia/Shanghai")
        self.assertEqual(body["duration_ms"], 1200)

    def test_daily_job_review_helpers(self):
        runs = self.client.list_job_runs(
            job_names=["pre_open_init", "post_close_settlement"],
            trade_date="20260731",
            limit=20,
        )
        self.assertEqual(runs[0]["target_trade_date"], "20260731")
        method, path, query, _body = RelayHandler.requests[-1]
        self.assertEqual((method, path), ("GET", "/v1/jobs/runs"))
        self.assertEqual(query["job_name"], ["pre_open_init,post_close_settlement"])
        self.assertEqual(query["limit"], ["20"])
        review = self.client.get_daily_review_report(trade_date="20260731")
        self.assertEqual(review["status"], "passed")
        self.assertEqual(RelayHandler.requests[-1][1], "/v1/reconciliations/review-report")

    def test_performance_meridian_and_reconciliation_helpers(self):
        daily = self.client.get_performance_daily(trade_date="20260612")
        self.assertEqual(daily["net_asset"], 123.45)
        contributions = self.client.get_performance_contributions(trade_date="20260612")
        self.assertEqual(contributions["contributions"][0]["strategy_type"], "stock_cross_section")
        quality = self.client.get_trade_quality(date_from="20260612", date_to="20260613")
        self.assertEqual(quality["summary"]["executed_order_rate"], 0.8)
        self.assertEqual(RelayHandler.requests[-1][2]["date_from"], ["20260612"])
        with self.assertRaises(ValueError):
            self.client.get_trade_quality(trade_date="20260612", date_from="20260612")
        series = self.client.get_performance_series(date_from="20260612", date_to="20260612", benchmark_security_id="000300.SH")
        self.assertEqual(series["series"][0]["trade_date"], "20260612")
        csv_text = self.client.get_performance_series_csv(date_from="20260612", date_to="20260612", benchmark_security_id="000300.SH")
        self.assertIn("account_id,trade_date,net_asset", csv_text)
        self.assertEqual(RelayHandler.requests[-2][2]["benchmark_security_id"], ["000300.SH"])
        self.assertEqual(RelayHandler.requests[-1][2]["benchmark_security_id"], ["000300.SH"])
        preview = self.client.preview_economic_nav(trade_date="20260612")
        self.assertFalse(preview["persisted"])
        cost_preview = self.client.preview_cost_ledger(trade_date="20260612")
        self.assertFalse(cost_preview["persisted"])
        self.assertEqual(cost_preview["status"], "calculated")
        cost_rebuilt = self.client.rebuild_cost_ledger(trade_date="20260612")
        self.assertTrue(cost_rebuilt["persisted"])
        self.assertEqual(RelayHandler.requests[-1][2]["trade_date"], ["20260612"])
        rebuilt = self.client.rebuild_economic_nav(trade_date="20260612", status="finalized")
        self.assertTrue(rebuilt["persisted"])
        reconcile_preview = self.client.preview_economic_nav_reconciliation(trade_date="20260612", observed_trade_date="20260615")
        self.assertFalse(reconcile_preview["persisted"])
        self.assertEqual(reconcile_preview["observed_trade_date"], "20260615")
        reconcile_rebuild = self.client.rebuild_economic_nav_reconciliation(trade_date="20260612", observed_trade_date="20260615")
        self.assertTrue(reconcile_rebuild["persisted"])
        confirmed = self.client.confirm_nav_reconciliation(trade_date="20260612", operator="tester", note="ok", force=True)
        self.assertEqual(confirmed["status"], "confirmed")
        blocked = self.client.block_nav_reconciliation(trade_date="20260612", operator="risk", reconciliation_id="nav-recon-1")
        self.assertEqual(blocked["status"], "blocked")
        navs = self.client.list_economic_nav(trade_date="20260612")
        self.assertEqual(navs[0]["status"], "provisional")
        reconciliations = self.client.list_nav_reconciliations(trade_date="20260612")
        self.assertEqual(reconciliations[0]["status"], "auto_completed")
        breaks = self.client.list_reconciliation_breaks(run_id="run-1", status="open")
        self.assertEqual(breaks[0]["run_id"], "run-1")
        bars = self.client.get_meridian_bars(security_id="600000.SH", trade_date="20260612")
        self.assertEqual(bars["data"][0]["close"], 9.46)
        factors = self.client.get_meridian_adjust_factors(security_id="600000.SH", start_date="20260601", end_date="20260612")
        self.assertEqual(factors["data"][0]["adj_factor"], 1.2345)
        components = self.client.get_meridian_etf_components(
            security_id="588200.SH",
            security_id_pattern="588*.SH",
            trade_date="20260729",
        )
        self.assertEqual(components["data"][0]["component_security_id"], "688361.SH")
        cash = self.client.get_meridian_etf_cash_components(security_ids=["588200.SH"], trade_date="20260729")
        self.assertEqual(cash["data"][0]["unit_subscribe_redeem"], "4500000")
        pcf_status = self.client.get_meridian_etf_pcf_status()
        self.assertEqual(pcf_status["data"]["state"]["status"], "success")

        requests = RelayHandler.requests[-5:]
        self.assertEqual(requests[0][1], "/v1/meridian/market/bars")
        self.assertEqual(requests[0][2]["trade_date"], ["20260612"])
        self.assertEqual(requests[1][1], "/v1/meridian/metadata/adjust-factors")
        self.assertEqual(requests[1][2]["start_date"], ["20260601"])
        self.assertEqual(requests[2][1], "/v1/meridian/market/etf-components")
        self.assertEqual(requests[2][2]["security_id_pattern"], ["588*.SH"])
        self.assertEqual(requests[3][2]["security_ids"], ["588200.SH"])
        self.assertEqual(requests[4][1], "/v1/meridian/market/etf-pcf-status")

    def test_record_settlement_snapshot(self):
        result = self.client.record_settlement_snapshot(
            trade_date="20260612",
            account_ids=["acct-1"],
            run_id="settlement-20260612",
            captured_at="2026-06-12T15:01:04+08:00",
            snapshot_only=True,
            dry_run=True,
        )

        self.assertEqual(result["run_id"], "settlement-20260612")
        method, path, _query, body = RelayHandler.requests[-1]
        self.assertEqual((method, path), ("POST", "/v1/settlements/snapshots"))
        self.assertEqual(body["trade_date"], "20260612")
        self.assertEqual(body["account_ids"], ["acct-1"])
        self.assertEqual(body["captured_at"], "2026-06-12T15:01:04+08:00")
        self.assertTrue(body["snapshot_only"])
        self.assertTrue(body["dry_run"])

    def test_submit_order_generates_traceable_ids(self):
        receipt = self.client.submit_order(
            symbol="600000",
            exchange="SH",
            side="B",
            price=9.67,
            qty=100,
            trade_date="20260724",
            strategy_type="etf_t0",
            strategy_id="etf-arb",
            basket_id="basket-1",
            t0_order_group_id="t0-1",
        )
        self.assertTrue(receipt.gateway_order_id.startswith("sdk-gw-acct-1-"))
        self.assertEqual(receipt.status, "created")
        method, path, _query, body = RelayHandler.requests[-1]
        self.assertEqual((method, path), ("POST", "/v1/orders"))
        self.assertEqual(body["account_id"], "acct-1")
        self.assertEqual(body["idempotency_key"], f"order:acct-1:{body['gateway_order_id']}")
        self.assertEqual(body["trade_date"], "20260724")
        self.assertEqual(body["strategy_type"], "etf_t0")
        self.assertEqual(body["strategy_id"], "etf-arb")
        self.assertEqual(body["basket_id"], "basket-1")
        self.assertEqual(body["t0_order_group_id"], "t0-1")

    def test_submit_order_replay_marker(self):
        receipt = self.client.submit_order(
            symbol="600000",
            exchange="SH",
            side="B",
            price=9.67,
            qty=100,
            gateway_order_id="gw-replay",
            client_order_id="client-replay",
            idempotency_key="idem-replay",
        )

        self.assertTrue(receipt.replayed)
        self.assertEqual(receipt.status, "cancelled")

    def test_refresh_and_cancel(self):
        self.assertEqual(self.client.refresh_orders().action, "order.list.query")
        self.assertEqual(self.client.refresh_fills().action, "fill.list.query")
        self.assertEqual(self.client.refresh_fees().action, "fee.list.query")
        self.assertEqual(self.client.cancel_order("gw-1").gateway_order_id, "gw-1")

    def test_list_order_fees(self):
        fees = self.client.list_order_fees(
            trade_date="20260801",
            gateway_order_id="gw-fee-1",
            fee_complete=True,
        )
        self.assertEqual(len(fees), 1)
        self.assertEqual(fees[0].gateway_order_id, "gw-fee-1")
        self.assertEqual(fees[0].total_fee, 6.25)
        self.assertTrue(fees[0].fee_complete)
        method, path, query, _body = RelayHandler.requests[-1]
        self.assertEqual((method, path), ("GET", "/v1/accounts/acct-1/fees"))
        self.assertEqual(query["fee_complete"], ["true"])

    def test_wait_order_terminal(self):
        order = self.client.wait_order_terminal("gw-1", timeout=1, poll_interval=0.01)
        self.assertTrue(order.is_terminal)
        self.assertEqual(order.filled_qty, 100)

    def test_error_mapping(self):
        with self.assertRaises(RelayIdempotencyError):
            self.client._request("POST", "/v1/error", json_body={})
        with self.assertRaises(RelayBrokerNotReadyError):
            self.client._request("POST", "/v1/broker-not-ready", json_body={})

        self.assertIsInstance(
            error_from_payload({"error": {"code": "BROKER_CANCEL_REJECTED", "message": "cancel rejected"}}),
            RelayCancelRejectedError,
        )
        self.assertIsInstance(
            error_from_payload({"error": {"code": "COMMAND_OUTCOME_UNKNOWN", "message": "reconcile first"}}),
            RelayCommandOutcomeUnknownError,
        )
        self.assertIsInstance(
            error_from_payload({"error": {"code": "QUERY_INTERRUPTED", "message": "retry query"}}),
            RelayQueryInterruptedError,
        )

    def test_sse_parser(self):
        stream = BytesIO(
            b'event: order.changed\n'
            b'data: {"account_ids":["acct-1"],"time":"2026-06-14T00:00:00Z","data":{"orders":1}}\n'
            b"\n"
        )
        event = next(iter_sse_events(stream))
        self.assertEqual(event.event_type, "order.changed")
        self.assertEqual(event.account_ids, ("acct-1",))
        self.assertEqual(event.data["orders"], 1)

    def test_order_status_callback_fetches_orders_after_event(self):
        seen = []

        subscription = self.client.on_order_status(
            lambda order, event: seen.append((order, event.event_type)),
            gateway_order_id="gw-1",
        )
        subscription.join(timeout=2)

        self.assertFalse(subscription.is_alive)
        self.assertIsNone(subscription.error)
        self.assertEqual(len(seen), 1)
        self.assertEqual(seen[0][0].status, "filled")
        self.assertEqual(seen[0][1], "order.changed")
        self.assertIn(("GET", "/v1/events/stream", {"account_id": ["acct-1"]}, None), RelayHandler.requests)

    def test_fill_callback_fetches_fills_after_event(self):
        seen = []

        self.client.watch_fills(lambda fill, event: seen.append((fill, event.event_type)))

        self.assertEqual(len(seen), 1)
        self.assertEqual(seen[0][0].fill_id, "fill-1")
        self.assertEqual(seen[0][1], "fill.changed")

    def test_fill_callback_allows_same_fill_id_on_different_orders(self):
        seen = []

        self.client.watch_fills(
            lambda fill, event: seen.append((fill.gateway_order_id, fill.fill_id, fill.qty)),
            symbol="dup-fill",
        )

        self.assertEqual(seen, [("gw-a", "reused-fill", 100), ("gw-b", "reused-fill", 200)])

    def test_cancel_rejected_callback_receives_attempt_context(self):
        seen = []

        self.client.watch_cancel_rejections(
            lambda event: seen.append(event.data["cancel_attempt"]),
            gateway_order_id="gw-cancel-rejected",
        )

        self.assertEqual(len(seen), 1)
        self.assertEqual(seen[0]["code"], "BROKER_CANCEL_REJECTED")

    def test_callback_keys_include_trade_date(self):
        self.assertNotEqual(
            _order_key(Order(account_id="acct-1", trade_date="2026-07-28", gateway_order_id="gw-1")),
            _order_key(Order(account_id="acct-1", trade_date="2026-07-29", gateway_order_id="gw-1")),
        )
        self.assertNotEqual(
            _fill_key(Fill(account_id="acct-1", trade_date="2026-07-28", gateway_order_id="gw-1", fill_id="fill-1")),
            _fill_key(Fill(account_id="acct-1", trade_date="2026-07-29", gateway_order_id="gw-1", fill_id="fill-1")),
        )


if __name__ == "__main__":
    unittest.main()
