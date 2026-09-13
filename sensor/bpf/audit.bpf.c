#include <linux/bpf.h>
#include <linux/pkt_cls.h>
#include <linux/if_ether.h>
#include <linux/ip.h>
#include <linux/in.h>
#include <linux/tcp.h>
#include <linux/udp.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

struct flow_key {
    __u8 src_ip[4];
    __u8 dst_ip[4];
    __u16 src_port;
    __u16 dst_port;
    __u8 proto;
    __u8 pad[3];
};

struct flow_val {
    __u64 start_ns;
    __u64 last_ns;
    __u64 bytes_tx;
    __u64 bytes_rx;
    __u64 packets_tx;
    __u64 packets_rx;
    __u8 ended;
    __u8 pad[7];
};

struct {
    __uint(type, BPF_MAP_TYPE_LRU_HASH);
    __uint(max_entries, 65536);
    __type(key, struct flow_key);
    __type(value, struct flow_val);
} flows SEC(".maps");

static __always_inline int parse_ipv4(struct __sk_buff *skb, void **data_out, void **end_out, struct iphdr **iph_out, __u64 *off_out) {
    void *data = (void *)(long)skb->data;
    void *end = (void *)(long)skb->data_end;
    __u64 off = 0;
    if (data + 1 > end) return -1;
    __u8 first = *(__u8 *)data;
    if ((first >> 4) != 4) {
        struct ethhdr *eth = data;
        if ((void *)(eth + 1) > end) return -1;
        if (eth->h_proto != bpf_htons(ETH_P_IP)) return -1;
        off = sizeof(*eth);
    }
    struct iphdr *iph = data + off;
    if ((void *)(iph + 1) > end) return -1;
    if (iph->version != 4 || iph->ihl < 5) return -1;
    if (data + off + iph->ihl * 4 > end) return -1;
    *data_out = data; *end_out = end; *iph_out = iph; *off_out = off;
    return 0;
}

static __always_inline int key_from_packet(struct __sk_buff *skb, struct flow_key *key, int reverse, __u8 *tcp_flags) {
    void *data, *end; struct iphdr *iph; __u64 off;
    if (parse_ipv4(skb, &data, &end, &iph, &off) < 0) return -1;
    if (iph->protocol != IPPROTO_TCP && iph->protocol != IPPROTO_UDP) return -1;
    __u64 l4off = off + iph->ihl * 4;
    __u16 sport = 0, dport = 0; *tcp_flags = 0;
    if (iph->protocol == IPPROTO_TCP) {
        struct tcphdr *tcp = data + l4off;
        if ((void *)(tcp + 1) > end) return -1;
        sport = bpf_ntohs(tcp->source); dport = bpf_ntohs(tcp->dest);
        if (tcp->fin) *tcp_flags |= 0x01;
        if (tcp->syn) *tcp_flags |= 0x02;
        if (tcp->rst) *tcp_flags |= 0x04;
        if (tcp->ack) *tcp_flags |= 0x10;
    } else {
        struct udphdr *udp = data + l4off;
        if ((void *)(udp + 1) > end) return -1;
        sport = bpf_ntohs(udp->source); dport = bpf_ntohs(udp->dest);
    }
    if (!reverse) {
        __builtin_memcpy(key->src_ip, &iph->saddr, 4);
        __builtin_memcpy(key->dst_ip, &iph->daddr, 4);
        key->src_port = sport; key->dst_port = dport;
    } else {
        __builtin_memcpy(key->src_ip, &iph->daddr, 4);
        __builtin_memcpy(key->dst_ip, &iph->saddr, 4);
        key->src_port = dport; key->dst_port = sport;
    }
    key->proto = iph->protocol;
    return 0;
}

SEC("tc")
int audit_ingress(struct __sk_buff *skb) {
    struct flow_key key = {}; __u8 flags = 0;
    if (key_from_packet(skb, &key, 0, &flags) < 0) return TC_ACT_OK;
    __u64 now = bpf_ktime_get_ns();
    struct flow_val *v = bpf_map_lookup_elem(&flows, &key);
    int new_tcp = key.proto == IPPROTO_TCP && (flags & 0x02) && !(flags & 0x10);
    if (!v || new_tcp) {
        struct flow_val nv = {.start_ns = now, .last_ns = now, .bytes_tx = skb->len, .packets_tx = 1};
        if (flags & (0x01 | 0x04)) nv.ended = 1;
        bpf_map_update_elem(&flows, &key, &nv, BPF_ANY);
    } else {
        v->last_ns = now; __sync_fetch_and_add(&v->bytes_tx, skb->len); __sync_fetch_and_add(&v->packets_tx, 1);
        if (flags & (0x01 | 0x04)) v->ended = 1;
    }
    return TC_ACT_OK;
}

SEC("tc")
int audit_egress(struct __sk_buff *skb) {
    struct flow_key key = {}; __u8 flags = 0;
    if (key_from_packet(skb, &key, 1, &flags) < 0) return TC_ACT_OK;
    struct flow_val *v = bpf_map_lookup_elem(&flows, &key);
    if (!v) return TC_ACT_OK;
    __u64 now = bpf_ktime_get_ns();
    v->last_ns = now; __sync_fetch_and_add(&v->bytes_rx, skb->len); __sync_fetch_and_add(&v->packets_rx, 1);
    if (flags & (0x01 | 0x04)) v->ended = 1;
    return TC_ACT_OK;
}

char LICENSE[] SEC("license") = "GPL";
