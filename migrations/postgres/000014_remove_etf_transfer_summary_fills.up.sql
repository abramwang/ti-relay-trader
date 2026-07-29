DELETE FROM fills
WHERE fill_id LIKE 'relay-summary:%'
    AND business_type = 'E'
    AND trade_side IN ('P', 'R');
