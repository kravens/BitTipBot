#!/usr/bin/env python3
import requests
from datetime import datetime, timedelta
from collections import defaultdict
from typing import Dict, List, Optional
import json

# Configuration
LNBITS_URL = "https://your-lnbits-instance.com"  # Replace with your LNbits URL
ADMIN_KEY = "your_admin_key_here"  # Replace with your admin key from config.yaml

# Headers for API requests
headers = {
    "X-Api-Key": ADMIN_KEY,
    "Content-type": "application/json"
}

def get_users() -> List[Dict]:
    """Get all users from LNbits usermanager"""
    url = f"{LNBITS_URL}/usermanager/api/v1/users"
    response = requests.get(url, headers=headers)
    if response.status_code != 200:
        print(f"Error getting users: {response.text}")
        return []
    return response.json()

def get_user_wallets(user_id: str) -> List[Dict]:
    """Get all wallets for a specific user"""
    url = f"{LNBITS_URL}/usermanager/api/v1/wallets/{user_id}"
    response = requests.get(url, headers=headers)
    if response.status_code != 200:
        print(f"Error getting wallets for user {user_id}: {response.text}")
        return []
    return response.json()

def get_wallet_transactions(wallet: Dict) -> List[Dict]:
    """Get all transactions for a wallet using its admin key"""
    wallet_headers = {
        "X-Api-Key": wallet["adminkey"],
        "Content-type": "application/json"
    }
    url = f"{LNBITS_URL}/api/v1/payments"
    response = requests.get(url, headers=wallet_headers)
    if response.status_code != 200:
        print(f"Error getting transactions for wallet {wallet['id']}: {response.text}")
        return []
    return response.json()

def get_telegram_username(wallet_name: str) -> Optional[str]:
    """Extract Telegram username from wallet name if possible"""
    # Wallet names are typically in format: "123456789 (@username)"
    if "(@" in wallet_name and ")" in wallet_name:
        return wallet_name.split("(@")[1].strip(")")
    return wallet_name

def analyze_faucet_transactions(all_transactions: List[Dict], all_wallets: Dict[str, Dict]) -> tuple[Dict, Dict]:
    """
    Analyze faucet transactions to get:
    1. Total amount distributed per user
    2. Total amount received per user
    Only considers transactions from the last 12 months
    """
    twelve_months_ago = datetime.now() - timedelta(days=365)
    
    # Track distributions and receipts
    distributions = defaultdict(int)  # username -> total amount
    receipts = defaultdict(int)      # username -> total amount
    
    for wallet_id, wallet in all_wallets.items():
        username = get_telegram_username(wallet["name"])
        
        for payment in all_transactions.get(wallet_id, []):
            # Check if it's a faucet transaction (adjust the memo check based on your actual memo format)
            if "faucet" not in payment.get("memo", "").lower():
                continue
                
            # Convert timestamp to datetime
            payment_time = datetime.fromtimestamp(payment["time"])
            if payment_time < twelve_months_ago:
                continue
                
            amount = payment["amount"]
            
            if payment.get("out", False):  # Outgoing payment (distribution)
                distributions[username] += amount
            else:  # Incoming payment (receipt)
                receipts[username] += amount
    
    return dict(distributions), dict(receipts)

def format_stats(stats: Dict[str, int], title: str) -> str:
    """Format statistics for display"""
    result = [f"\n{title}:"]
    for username, amount in sorted(stats.items(), key=lambda x: x[1], reverse=True):
        # Convert amount from millisats to sats
        sats = amount / 1000
        result.append(f"  {username}: {sats:,.0f} sats")
    return "\n".join(result)

def main():
    print("Fetching faucet statistics from LNbits...")
    
    # Get all users
    users = get_users()
    if not users:
        print("No users found!")
        return
        
    # Get all wallets and their transactions
    all_wallets = {}  # wallet_id -> wallet
    all_transactions = {}  # wallet_id -> transactions
    
    for user in users:
        wallets = get_user_wallets(user["id"])
        for wallet in wallets:
            all_wallets[wallet["id"]] = wallet
            transactions = get_wallet_transactions(wallet)
            all_transactions[wallet["id"]] = transactions
    
    # Analyze transactions
    distributions, receipts = analyze_faucet_transactions(all_transactions, all_wallets)
    
    # Display results
    print("\nFaucet Statistics for the Last 12 Months:")
    print("-" * 50)
    
    print(format_stats(distributions, "Total Amount Distributed per User"))
    print(format_stats(receipts, "Total Amount Received per User"))
    
    # Example output format:
    """
    Faucet Statistics for the Last 12 Months:
    --------------------------------------------------
    
    Total Amount Distributed per User:
      @alice: 1,000,000 sats
      @bob: 500,000 sats
      @charlie: 250,000 sats
    
    Total Amount Received per User:
      @david: 100,000 sats
      @eve: 75,000 sats
      @frank: 50,000 sats
    """

if __name__ == "__main__":
    main() 