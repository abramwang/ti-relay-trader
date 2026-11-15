import copy
from decimal import Decimal
import importlib.util
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("daily_attribution", ROOT / "scripts/audit-fuying13-daily-attribution.py")
M = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(M)


class DailyAttributionTest(unittest.TestCase):
    def fixture(self, overnight=False, carried=False):
        date, source = "2026-10-15", "2026-10-14" if overnight else "2026-10-15"
        cycle = date + "/510310.SH"
        tr = {"redemption_quantity":100,"acquisition_quantity":100,"acquisition_gross_cents":100000,
              "component_sale_cents":95000 if carried else 100400,"ordinary_execution_fee_cents":300 if carried else 200,"acquisition_date":source}
        p = {"cycle_id":cycle,"trading":tr,"observed_net_cash_cents":10000 if carried else 500,
             "net_cycle_pnl_cents":10200 if carried else 700,"quality_flags":["unverified"]}
        row = {"security_id":"510310.SH","strategy_type":"etf_redemption_t0","strategy_id":"parent","redemption_quantity":100,
               "linked_component_sales":950 if carried else 1004,"buy_amount":0 if overnight else 1000,"open_value":900 if overnight else 0,"close_value":0,"net_contribution":-20}
        days = [{"trade_date":date,"contributions":[row]}]
        members = [{"cycle_id":cycle,"trade_date":source,"gateway_order_id":"buy","security_id":"510310.SH","side":"B","fill_id":"b","quantity":100,"gross_micros":1000000000}]
        fees = [{"trade_date":source,"gateway_order_id":"buy","security_id":"510310.SH","side":"B","quantity":100,"oc_gross_micros":1000000000,"status":"matched","fee_cents":200}]
        if overnight:
            days.insert(0,{"trade_date":source,"contributions":[{"security_id":"510310.SH","strategy_type":"etf_redemption_t0","strategy_id":"parent","redemption_quantity":0,"buy_quantity":100,"close_quantity":100,"buy_amount":1000,"sell_amount":0,"open_value":0,"close_value":900,"net_contribution":-102}]})
        if carried:
            component = {"security_id":"600000.SH","strategy_type":"etf_redemption_t0","strategy_id":"ETF_T0_COMPONENT:"+date+":parent","open_quantity":0,"close_quantity":10,"buy_quantity":0,"sell_quantity":0,"open_value":0,"close_value":50,"buy_amount":0,"sell_amount":0,"net_contribution":0}
            row["buy_quantity"],row["close_quantity"]=100,0
            days[0]["contributions"].append(component)
            later = {**component,"open_quantity":10,"close_quantity":0,"sell_quantity":10,"open_value":50,"close_value":0,"sell_amount":55,"net_contribution":4}
            days.append({"trade_date":"2026-10-16","contributions":[later]})
            fees.append({"trade_date":"2026-10-16","gateway_order_id":"sell","security_id":"600000.SH","side":"S","quantity":10,"oc_gross_micros":55000000,"status":"matched","fee_cents":100})
        return [p],days,members,fees

    def test_same_day_and_overnight_conserve_cycle(self):
        for overnight in (False,True):
            data=self.fixture(overnight=overnight)
            before=copy.deepcopy(data)
            parts=M.daily_cycle_parts(*data)
            self.assertEqual(sum(p["net"] for p in parts),Decimal(7))
            self.assertEqual(sum(p["fee"] for p in parts),Decimal(2))
            self.assertEqual(data,before)
            if overnight:
                self.assertEqual(parts[0]["net"],Decimal(-102))
                self.assertEqual(parts[1]["net"],Decimal(109))

    def test_component_close_value_then_release_not_duplicate_cash(self):
        parts=M.daily_cycle_parts(*self.fixture(carried=True))
        self.assertEqual(sum(p["net"] for p in parts),Decimal(102))
        component=[p for p in parts if p["kind"]=="carried_component"]
        self.assertEqual([p["net"] for p in component],[Decimal(50),Decimal(4)])

    def test_ambiguous_or_incomplete_evidence_rejected(self):
        for kind in ("fill","partial","fee","gross","parent","cost","profit","duplicate-row","quantity","unexpected-date","negative-fee"):
            with self.subTest(kind=kind):
                p,d,m,f=self.fixture()
                if kind=="fill":m.append(copy.deepcopy(m[0]))
                elif kind=="partial":m[0]["quantity"]-=1
                elif kind=="fee":f[0]["status"]="unmatched"
                elif kind=="gross":f[0]["oc_gross_micros"]+=1
                elif kind=="parent":d[0]["contributions"][0]["security_id"]="159915.SZ"
                elif kind=="cost":d[0]["contributions"][0]["buy_amount"]+=1
                elif kind=="profit":p[0]["net_cycle_pnl_cents"]+=1
                elif kind=="duplicate-row":d[0]["contributions"].append(copy.deepcopy(d[0]["contributions"][0]))
                elif kind=="quantity":d[0]["contributions"][0]["redemption_quantity"]+=1
                elif kind=="unexpected-date":m[0]["trade_date"]="2026-10-13"
                elif kind=="negative-fee":f[0]["fee_cents"]=-1
                with self.assertRaises(ValueError):M.daily_cycle_parts(p,d,m,f)

    def test_component_marks_and_fees_rejected(self):
        for kind in ("open","close","fee","gross","quantity"):
            with self.subTest(kind=kind):
                p,d,m,f=self.fixture(carried=True)
                if kind=="open":d[1]["contributions"][0]["open_value"]+=1
                elif kind=="close":d[1]["contributions"][0]["close_quantity"]=1
                elif kind=="fee":f[-1]["fee_cents"]=None
                elif kind=="gross":f[-1]["oc_gross_micros"]+=1
                elif kind=="quantity":f[-1]["quantity"]-=1
                with self.assertRaises(ValueError):M.daily_cycle_parts(p,d,m,f)

    def test_money_rejects_nonfinite_and_unsupported(self):
        for n in (True,Decimal("NaN"),Decimal("Infinity"),Decimal("1e12"),1.2,None):
            with self.assertRaises(ValueError):M.number(n)

    def test_sealed_private_evidence(self):
        if not all((ROOT/p).exists() for p,_ in M.INPUTS.values()):
            self.skipTest("private evidence not in repository")
        sources={k:M.checked_input(ROOT/p,h) for k,(p,h) in M.INPUTS.items()}
        args=[sources[k] for k in ("cycles","cost","fees","receipts")]
        r=M.build_report(*args)
        self.assertEqual(r["cycle_count"],14)
        self.assertEqual(r["summary"]["bridged_days"],21)
        self.assertEqual(r["summary"]["max_absolute_remaining_comparison"],Decimal("0.047621"))
        self.assertFalse(r["economic_nav_publishable"])
        for d in r["days"]:
            if d["status"]=="conditional_bridge_not_final":
                self.assertEqual(sum(d["conditional_strategies"].values()),d["corrected_net"])
                self.assertFalse(d["asset_component_classification_verified"])
            else:
                self.assertNotIn("remaining_residual",d)
        self.assertEqual(r["summary"]["uncovered_dates"],["2026-11-10","2026-11-11","2026-11-12","2026-11-13"])
        for kind in ("account","duplicate-receipt","principal","unsafe","final"):
            changed=copy.deepcopy(args)
            if kind=="account":changed[1]["account_id"]="other"
            elif kind=="duplicate-receipt":changed[3]["repo_receipt_evidence"].append(copy.deepcopy(changed[3]["repo_receipt_evidence"][0]))
            elif kind=="principal":changed[3]["repo_receipt_evidence"][0]["principal"]="1"
            elif kind=="unsafe":changed[0]["database_writes"]=1
            elif kind=="final":changed[0]["economic_nav_publishable"]=True
            with self.subTest(kind=kind),self.assertRaises(ValueError):M.build_report(*changed)

    def continuation_fixture(self):
        date, source = "2026-11-10", "2026-11-09"
        extension = {"schema":"relay.formal_daily_review.v1","account_id":M.ACCOUNT,"database_writes":0,"broker_commands":0,
                     "environment":"production","database_read_only":True,"cost_chain_complete":True,"error":"",
                     "cost_evidence":{"2026-11-10":"stored_zero_origin_anchor","2026-11-11":"replay_matches_formal_cost","2026-11-12":"replay_matches_formal_cost","2026-11-13":"read_only_replay_from_formal_previous_day_not_persisted"},
                     "contributions":[{"trade_date":d} for d in M.EXTENSION_DATES[1:]],"orders":[],"fills":[]}
        cash = {"account_id":M.ACCOUNT,"database_writes":0,"broker_commands":0,"new_cash_rows":[]}
        fees = {"orders":[]}
        for n, d in enumerate([source]+M.EXTENSION_DATES):
            order = str(n).zfill(10)
            extension["orders"].append({"account_id":M.ACCOUNT,"trade_date":d,"gateway_order_id":order,"order_stream_id":"11001"+order,
                                        "symbol":"204001","exchange":"SH","trade_side":"S","business_type":"S","is_terminal":True,"status":"filled","cum_filled_qty":100})
            extension["fills"].append({"account_id":M.ACCOUNT,"trade_date":d,"gateway_order_id":order,"fill_id":"f"+order,"symbol":"204001","exchange":"SH","trade_side":"S","business_type":"S","qty":100})
            fees["orders"].append({"account_id":M.ACCOUNT,"trade_date":d.replace("-", ""),"broker_order_id":order,"security_id":"204001.SH","side":"融券","fee_amount_resolved":True,"unresolved_groups":[],"quantity":10,"gross_cents":1000000,"fee_cents":10})
            if n<4:
                posted=M.EXTENSION_DATES[n]
                raw={"交易日期":posted.replace("-", ""),"业务科目":"回购融券购回资金划入","操作摘要":"融券购回204001:成交日期"+d.replace("-", "")+"，申报委托号"+order,
                     "投资者":M.ACCOUNT,"流水号":"r"+order,"股票代码":"204001","入金":"10001.25","出金":"0"}
                cash["new_cash_rows"].append({"account_id":M.ACCOUNT,"broker_flow_id":"r"+order,"actual_amount":"10001.25","raw_fields":raw})
        return {"contributions":[{"trade_date":date}]},{"repo_receipt_evidence":[]},extension,cash,fees

    def test_continuation_receipts_use_identity_quantity_and_principal(self):
        args=self.continuation_fixture()
        before=copy.deepcopy(args)
        cost,receipts,fees,linked=M.continuation_evidence(*args)
        self.assertEqual(args,before)
        self.assertEqual(len(cost["contributions"]),4)
        self.assertEqual(len(receipts["repo_receipt_evidence"]),4)
        self.assertEqual(sum(r["received_interest"] for r in linked),Decimal(5))
        self.assertEqual(sum(fees.values()),Decimal("0.4"))

    def test_continuation_rejects_corrupt_or_ambiguous_evidence(self):
        mutations={
            "account":lambda a:a[2].update(account_id="other"),
            "write":lambda a:a[2].update(database_writes=1),
            "test":lambda a:a[2].update(environment="test"),
            "incomplete":lambda a:a[2].update(cost_chain_complete=False),
            "false-finality":lambda a:a[3].update(final_settlement_verified=True),
            "overlap":lambda a:a[0]["contributions"].append({"trade_date":"2026-11-11"}),
            "scope":lambda a:a[2]["cost_evidence"].update({"2026-11-13":"replay_matches_formal_cost"}),
            "missing-day":lambda a:a[2]["contributions"].pop(),
            "duplicate-fill":lambda a:a[2]["fills"].append(copy.deepcopy(a[2]["fills"][0])),
            "fill-quantity":lambda a:a[2]["fills"][0].update(qty=99),
            "duplicate-order":lambda a:a[2]["orders"].append(copy.deepcopy(a[2]["orders"][0])),
            "missing-order":lambda a:a[2]["orders"].pop(),
            "partial-order":lambda a:a[2]["orders"][0].update(status="working"),
            "statement-principal":lambda a:a[4]["orders"][0].update(gross_cents=999999),
            "statement-lots":lambda a:a[4]["orders"][0].update(quantity=11),
            "unresolved-fee":lambda a:a[4]["orders"][0].update(fee_amount_resolved=False),
            "duplicate-receipt":lambda a:a[3]["new_cash_rows"].append(copy.deepcopy(a[3]["new_cash_rows"][0])),
            "missing-receipt":lambda a:a[3]["new_cash_rows"].pop(),
            "receipt-amount":lambda a:a[3]["new_cash_rows"][0].update(actual_amount="10002"),
            "summary":lambda a:a[3]["new_cash_rows"][0]["raw_fields"].update({"操作摘要":"unknown"}),
            "receipt-account":lambda a:a[3]["new_cash_rows"][0]["raw_fields"].update({"投资者":"other"}),
        }
        for name,change in mutations.items():
            args=self.continuation_fixture()
            change(args)
            with self.subTest(name=name),self.assertRaises(ValueError):M.continuation_evidence(*args)

    def test_extended_private_evidence(self):
        files={**M.INPUTS,**M.EXTENSION_INPUTS}
        if not all((ROOT/p).exists() for p,_ in files.values()):
            self.skipTest("private evidence not in repository")
        s={k:M.checked_input(ROOT/p,h) for k,(p,h) in files.items()}
        args=[s[k] for k in ("cycles","cost","fees","receipts")]
        original=M.build_report(*args)
        extended=M.build_report(*args,s["formal_extension"],s["cash_extension"])
        self.assertEqual(extended["days"][:21],original["days"][:21])
        self.assertEqual(extended["daily_cycle_parts"],original["daily_cycle_parts"])
        self.assertEqual(extended["summary"]["bridged_days"],25)
        self.assertEqual(extended["summary"]["uncovered_dates"],[])
        self.assertEqual(extended["summary"]["max_absolute_remaining_comparison"],Decimal("0.047621"))
        self.assertEqual(extended["days"][-1]["repo_fee_correction"],Decimal("-113.17"))
        self.assertEqual(extended["days"][-1]["remaining_residual"],Decimal("0.040450"))
        self.assertIn("missing_repo_fee",extended["days"][-1]["original_quality_flags"])
        self.assertFalse(extended["economic_nav_publishable"])
        for d in extended["days"]:
            self.assertEqual(sum(d["conditional_strategies"].values()),d["corrected_net"])
        comparison=extended["statement_daily_comparison"]
        self.assertEqual(comparison["matched_orders"],341)
        self.assertEqual(comparison["matched_fills"],3211)
        self.assertEqual([d["statement_pnl_difference"] for d in comparison["days"]],[Decimal(0)]*4)
        self.assertEqual([d["unexplained_model_difference"] for d in comparison["days"]],[Decimal(0)]*4)
        self.assertEqual([d["statement_gross_minus_fill_gross"] for d in comparison["gross_differences"]],[Decimal("0.002"),Decimal("-0.004")])
        merged,_,_,_=M.continuation_evidence(s["cost"],s["receipts"],s["formal_extension"],s["cash_extension"],s["fees"])
        for kind in ("fill-quantity","duplicate-fill","missing-order","fee","external-flow","pnl"):
            ext,cash,fee=copy.deepcopy([s["formal_extension"],s["cash_extension"],s["fees"]])
            if kind=="fill-quantity":next(f for f in ext["fills"] if f["trade_date"]=="2026-11-13")["qty"]+=1
            elif kind=="duplicate-fill":ext["fills"].append(copy.deepcopy(next(f for f in ext["fills"] if f["trade_date"]=="2026-11-13")))
            elif kind=="missing-order":ext["orders"]=[o for o in ext["orders"] if o["trade_date"]!="2026-11-13"]
            elif kind=="fee":next(o for o in fee["orders"] if o["trade_date"]=="20261113")["fee_cents"]+=1
            elif kind=="external-flow":cash["days"]["20261113"]["funds"]["出金"]="5000000"
            elif kind=="pnl":cash["days"]["20261113"]["funds"]["当日盈亏"]="1"
            with self.subTest(kind=kind),self.assertRaises(ValueError):
                M.statement_daily_comparison(extended["days"],merged["contributions"],ext,cash,fee)
        changed=copy.deepcopy(s["cash_extension"])
        changed["days"]["20261113"]["funds"]["市值"]="20262745.00"
        drift=M.statement_daily_comparison(extended["days"],merged["contributions"],s["formal_extension"],changed,s["fees"])
        self.assertEqual(drift["days"][-1]["statement_pnl_difference"],Decimal(-1))
        self.assertEqual(drift["days"][-1]["unexplained_model_difference"],Decimal(-1))


if __name__ == "__main__":
    unittest.main()
