/**
 * BoxCloudAccountCard — for a bot that runs on the cloud: who the account
 * is, whether ascii.dev will start a box at all, and a link to fix it.
 * Without this a valid key and a blocked account look identical until a
 * turn fails with a 402.
 */

import { useQuery } from "@tanstack/react-query";

import { getBoxAccount } from "../../api/computer";
import { BoxPlanNotice, boxAccountLine } from "./BoxPlanNotice";

export function BoxCloudAccountCard({ slug }: { slug: string }) {
  const { data: account } = useQuery({
    queryKey: ["box-account", slug],
    queryFn: () => getBoxAccount(),
    staleTime: 30_000,
    refetchInterval: 60_000,
  });
  if (!account) return null;
  const line = boxAccountLine(account);
  if (account.canStart !== false && !line) return null;
  return (
    <div className="computer-card" data-testid="box-cloud-account">
      {line ? <p className="box-account-line">{line}</p> : null}
      <BoxPlanNotice account={account} compact={true} />
    </div>
  );
}
