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


if __name__ == "__main__":
    unittest.main()
