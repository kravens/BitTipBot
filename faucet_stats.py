#!/usr/bin/env python3
import requests
import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict
from typing import Dict, List, Optional
import json
import os

# SQLite database paths (from config.yaml)
DB_PATH = "data/bot.db"  # Main user database
TRANSACTIONS_PATH = "data/transactions.db"  # Transactions database

# Headers for API requests
#headers = {
#    "X-Api-Key": ADMIN_KEY,
#    "Content-type": "application/json"
#}

def get_transactions_from_sqlite() -> List[Dict]:
    """Get all faucet transactions from the local SQLite database"""
    if not os.path.exists(TRANSACTIONS_PATH):
        print(f"Error: Transactions database not found at {TRANSACTIONS_PATH}")
        return []
        
    conn = sqlite3.connect(TRANSACTIONS_PATH)
    conn.row_factory = sqlite3.Row
    cursor = conn.cursor()
    
    # Get transactions from the last week where type is 'faucet'
    twelve_months_ago = int((datetime.now() - timedelta(days=7)).timestamp())
    query = """
    SELECT 
        from_user as sender,
        to_user as recipient,
        amount,
        time,
        type,
        success,
        memo
    FROM transactions 
    WHERE type = 'faucet' 
    AND success = 1 
    AND time >= datetime(?, 'unixepoch')
    """
    
    try:
        cursor.execute(query, (twelve_months_ago,))
        rows = cursor.fetchall()
        return [dict(row) for row in rows]
    except sqlite3.Error as e:
        print(f"Error querying SQLite database: {e}")
        return []
    finally:
        conn.close()

def get_user_info_from_sqlite(user_id: str) -> Optional[Dict]:
    """Get user information from the local SQLite database"""
    if not os.path.exists(DB_PATH):
        print(f"Error: User database not found at {DB_PATH}")
        return None
        
    conn = sqlite3.connect(DB_PATH)
    conn.row_factory = sqlite3.Row
    cursor = conn.cursor()
    
    try:
        cursor.execute("""
            SELECT 
                name,
                telegram_username,
                wallet_id,
                wallet_name,
                wallet_balance
            FROM users 
            WHERE name = ? OR telegram_username = ?
        """, (user_id, user_id))
        row = cursor.fetchone()
        return dict(row) if row else None
    except sqlite3.Error as e:
        print(f"Error querying SQLite database: {e}")
        return None
    finally:
        conn.close()

def analyze_faucet_transactions() -> tuple[Dict[str, int], Dict[str, int], Dict[str, int]]:
    """
    Analyze faucet transactions to get:
    1. Total amount distributed per user
    2. Total amount received per user
    Combines data from both SQLite and LNbits API
    """
    # Track distributions and receipts
    distributions = defaultdict(int)  # username -> total amount
    receipts = defaultdict(int)      # username -> total amount
    balance = defaultdict(int)       # username -> total amount
    
    # Get transactions from SQLite
    transactions = get_transactions_from_sqlite()
    
    for tx in transactions:
        # Skip failed transactions
        if not tx['success']:
            continue
            
        # Get sender and recipient usernames
        sender = tx['sender']
        recipient = tx['recipient']
        amount = tx['amount']
        
        if "faucet" in tx['type'].lower():
            distributions[sender] += amount
            receipts[recipient] += amount
            balance[sender] += amount
            balance[recipient] += -amount
    
    return dict(distributions), dict(receipts), dict(balance)

def format_stats(stats: Dict[str, int], title: str) -> str:
    """Format statistics for display"""
    result = [f"\n{title}:"]
    for username, amount in sorted(stats.items(), key=lambda x: x[1], reverse=True):
        # Convert amount from sats to BTC for readability
        sats = amount
        result.append(f"  {username}: {sats:,.0f} sats")
    return "\n".join(result)

def main():
    print("Fetching faucet statistics...")
    
    # Analyze transactions from both sources
    distributions, receipts, balance = analyze_faucet_transactions()
    
    # Display results
    print("\nFaucet Statistics for the Last 7 Days:")
    print("-" * 50)
    
    print(format_stats(distributions, "Total Amount Distributed per User of last 7 days"))
    print(format_stats(receipts, "Total Amount Received per User of last 7 days"))
    print(format_stats(balance, "Balance of Faucets per User of last 7 days (negative = lurker)"))

# Run
main()
