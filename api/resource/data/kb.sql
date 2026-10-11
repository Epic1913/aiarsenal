-- ============================================================================
-- kb.sql — 知识库管理模块数据库与表结构
-- ============================================================================
-- 依赖：PostgreSQL 16 + pgvector（与 aiarsenal / aa_skill 同实例异库）
-- 关联：created_by 软引用 aiarsenal 库的 aa_user.user_id（跨库无 FK）
-- 约定：每表 id bigserial PK（内部）+ <实体>_id varchar(32) UNIQUE（对外业务 id）
-- 设计：文档（元数据+抽取全文）与分块（chunk + 语义向量）分表——检索按块向量
--       打分、聚合回文档，命中块内容即摘要；更新文档 = 重抽全文 + 重嵌全部分块
-- 数据：库内只存运行时数据（文档内容/向量均为本机导入，绝不进 git/种子）
-- 重建：psql -f resource/data/kb.sql（每表 DROP IF EXISTS，可重复跑）
-- 存量环境启用：先跑本文件（建库+建表+菜单种子），再把 aa_kb 全表 OWNER 收口给
--             aiarsenal（迁移以非 owner 连接执行，ALTER 须所有者）；vector 扩展
--             须 superuser 预装（迁移不含 CREATE EXTENSION，避免越权失败）
-- ============================================================================

-- ----------------------------------------------------------------------------
-- Part A — 独立数据库 aa_kb
-- ----------------------------------------------------------------------------

SELECT 'CREATE DATABASE aa_kb'
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'aa_kb')\gexec

\connect aa_kb

-- 以上为历史前导（gexec 建库 + connect 目标库）；以下正文为 pg_dump 固化版（2026-09 squash，已合并全部历史迁移）
-- PostgreSQL database dump
--

\restrict hEtrrYxAHnSPGJX4omlSw8pCdufI6YMok3FJkg65kE3IRmpFImgYxUcYXwqMfLh

-- Dumped from database version 15.19 (Homebrew)
-- Dumped by pg_dump version 15.19 (Homebrew)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

DROP INDEX IF EXISTS public.uq_aa_kb_dir_name;
DROP INDEX IF EXISTS public.idx_aa_kb_grant_target;
DROP INDEX IF EXISTS public.idx_aa_kb_document_type;
DROP INDEX IF EXISTS public.idx_aa_kb_document_hash;
DROP INDEX IF EXISTS public.idx_aa_kb_document_dir;
DROP INDEX IF EXISTS public.idx_aa_kb_document_created;
DROP INDEX IF EXISTS public.idx_aa_kb_dir_parent;
DROP INDEX IF EXISTS public.idx_aa_kb_chunk_doc;
DROP INDEX IF EXISTS public.idx_aa_kb_access_doc;
DROP INDEX IF EXISTS public.idx_aa_kb_access_created;
ALTER TABLE IF EXISTS ONLY public.aa_kb_grant DROP CONSTRAINT IF EXISTS aa_kb_grant_pkey;
ALTER TABLE IF EXISTS ONLY public.aa_kb_document DROP CONSTRAINT IF EXISTS aa_kb_document_pkey;
ALTER TABLE IF EXISTS ONLY public.aa_kb_document DROP CONSTRAINT IF EXISTS aa_kb_document_doc_id_key;
ALTER TABLE IF EXISTS ONLY public.aa_kb_dir DROP CONSTRAINT IF EXISTS aa_kb_dir_pkey;
ALTER TABLE IF EXISTS ONLY public.aa_kb_dir DROP CONSTRAINT IF EXISTS aa_kb_dir_dir_id_key;
ALTER TABLE IF EXISTS ONLY public.aa_kb_chunk DROP CONSTRAINT IF EXISTS aa_kb_chunk_pkey;
ALTER TABLE IF EXISTS ONLY public.aa_kb_access_log DROP CONSTRAINT IF EXISTS aa_kb_access_log_pkey;
ALTER TABLE IF EXISTS public.aa_kb_grant ALTER COLUMN id DROP DEFAULT;
ALTER TABLE IF EXISTS public.aa_kb_document ALTER COLUMN id DROP DEFAULT;
ALTER TABLE IF EXISTS public.aa_kb_dir ALTER COLUMN id DROP DEFAULT;
ALTER TABLE IF EXISTS public.aa_kb_chunk ALTER COLUMN id DROP DEFAULT;
ALTER TABLE IF EXISTS public.aa_kb_access_log ALTER COLUMN id DROP DEFAULT;
DROP SEQUENCE IF EXISTS public.aa_kb_grant_id_seq;
DROP TABLE IF EXISTS public.aa_kb_grant;
DROP SEQUENCE IF EXISTS public.aa_kb_document_id_seq;
DROP TABLE IF EXISTS public.aa_kb_document;
DROP SEQUENCE IF EXISTS public.aa_kb_dir_id_seq;
DROP TABLE IF EXISTS public.aa_kb_dir;
DROP SEQUENCE IF EXISTS public.aa_kb_chunk_id_seq;
DROP TABLE IF EXISTS public.aa_kb_chunk;
DROP SEQUENCE IF EXISTS public.aa_kb_access_log_id_seq;
DROP TABLE IF EXISTS public.aa_kb_access_log;
DROP EXTENSION IF EXISTS vector;
--
-- Name: vector; Type: EXTENSION; Schema: -; Owner: -
--

CREATE EXTENSION IF NOT EXISTS vector WITH SCHEMA public;


SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: aa_kb_access_log; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.aa_kb_access_log (
    id bigint NOT NULL,
    doc_id character varying(32) DEFAULT ''::character varying NOT NULL,
    user_id character varying(32) DEFAULT ''::character varying NOT NULL,
    action character varying(8) DEFAULT 'read'::character varying NOT NULL,
    keyword character varying(256) DEFAULT ''::character varying NOT NULL,
    hits integer DEFAULT 0 NOT NULL,
    source character varying(8) DEFAULT 'ui'::character varying NOT NULL,
    created_at timestamp without time zone DEFAULT now()
);


--
-- Name: aa_kb_access_log_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.aa_kb_access_log_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: aa_kb_access_log_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.aa_kb_access_log_id_seq OWNED BY public.aa_kb_access_log.id;


--
-- Name: aa_kb_chunk; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.aa_kb_chunk (
    id bigint NOT NULL,
    doc_id character varying(32) DEFAULT ''::character varying NOT NULL,
    seq integer DEFAULT 0 NOT NULL,
    content text DEFAULT ''::text NOT NULL,
    semantic_vector public.vector(1536),
    created_at timestamp without time zone DEFAULT now()
);


--
-- Name: aa_kb_chunk_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.aa_kb_chunk_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: aa_kb_chunk_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.aa_kb_chunk_id_seq OWNED BY public.aa_kb_chunk.id;


--
-- Name: aa_kb_dir; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.aa_kb_dir (
    id bigint NOT NULL,
    dir_id character varying(32) DEFAULT ''::character varying NOT NULL,
    name character varying(128) DEFAULT ''::character varying NOT NULL,
    parent_id character varying(32) DEFAULT ''::character varying NOT NULL,
    visibility smallint DEFAULT 2 NOT NULL,
    created_by character varying(32) DEFAULT ''::character varying NOT NULL,
    created_at timestamp without time zone DEFAULT now(),
    updated_at timestamp without time zone DEFAULT now()
);


--
-- Name: aa_kb_dir_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.aa_kb_dir_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: aa_kb_dir_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.aa_kb_dir_id_seq OWNED BY public.aa_kb_dir.id;


--
-- Name: aa_kb_document; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.aa_kb_document (
    id bigint NOT NULL,
    doc_id character varying(32) DEFAULT ''::character varying NOT NULL,
    title character varying(512) DEFAULT ''::character varying NOT NULL,
    summary text,
    file_name character varying(512) DEFAULT ''::character varying NOT NULL,
    file_type character varying(16) DEFAULT ''::character varying NOT NULL,
    file_size bigint DEFAULT 0 NOT NULL,
    file_hash character varying(64) DEFAULT ''::character varying NOT NULL,
    content_text text DEFAULT ''::text NOT NULL,
    char_count integer DEFAULT 0 NOT NULL,
    chunk_count integer DEFAULT 0 NOT NULL,
    embed_status smallint DEFAULT 0 NOT NULL,
    embed_error text,
    source character varying(16) DEFAULT 'ui'::character varying NOT NULL,
    storage_path character varying(512) DEFAULT ''::character varying NOT NULL,
    dir_id character varying(32) DEFAULT ''::character varying NOT NULL,
    visibility smallint DEFAULT 2 NOT NULL,
    created_by character varying(32) DEFAULT ''::character varying NOT NULL,
    created_at timestamp without time zone DEFAULT now(),
    updated_at timestamp without time zone DEFAULT now()
);


--
-- Name: aa_kb_document_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.aa_kb_document_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: aa_kb_document_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.aa_kb_document_id_seq OWNED BY public.aa_kb_document.id;


--
-- Name: aa_kb_grant; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.aa_kb_grant (
    id bigint NOT NULL,
    target_type character varying(8) DEFAULT ''::character varying NOT NULL,
    target_id character varying(32) DEFAULT ''::character varying NOT NULL,
    action character varying(8) DEFAULT 'read'::character varying NOT NULL,
    user_ids jsonb DEFAULT '[]'::jsonb NOT NULL,
    dept_id character varying(32) DEFAULT ''::character varying NOT NULL,
    role_ids jsonb DEFAULT '[]'::jsonb NOT NULL,
    post_ids jsonb DEFAULT '[]'::jsonb NOT NULL,
    exclude boolean DEFAULT false NOT NULL,
    group_id character varying(32) DEFAULT ''::character varying NOT NULL,
    set_id character varying(32) DEFAULT ''::character varying NOT NULL,
    created_by character varying(32) DEFAULT ''::character varying NOT NULL,
    created_at timestamp without time zone DEFAULT now()
);


--
-- Name: aa_kb_grant_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.aa_kb_grant_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: aa_kb_grant_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.aa_kb_grant_id_seq OWNED BY public.aa_kb_grant.id;


--
-- Name: aa_kb_access_log id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.aa_kb_access_log ALTER COLUMN id SET DEFAULT nextval('public.aa_kb_access_log_id_seq'::regclass);


--
-- Name: aa_kb_chunk id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.aa_kb_chunk ALTER COLUMN id SET DEFAULT nextval('public.aa_kb_chunk_id_seq'::regclass);


--
-- Name: aa_kb_dir id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.aa_kb_dir ALTER COLUMN id SET DEFAULT nextval('public.aa_kb_dir_id_seq'::regclass);


--
-- Name: aa_kb_document id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.aa_kb_document ALTER COLUMN id SET DEFAULT nextval('public.aa_kb_document_id_seq'::regclass);


--
-- Name: aa_kb_grant id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.aa_kb_grant ALTER COLUMN id SET DEFAULT nextval('public.aa_kb_grant_id_seq'::regclass);


--
-- Data for Name: aa_kb_access_log; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.aa_kb_access_log (id, doc_id, user_id, action, keyword, hits, source, created_at) FROM stdin;
\.


--
-- Data for Name: aa_kb_chunk; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.aa_kb_chunk (id, doc_id, seq, content, semantic_vector, created_at) FROM stdin;
\.


--
-- Data for Name: aa_kb_dir; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.aa_kb_dir (id, dir_id, name, parent_id, visibility, created_by, created_at, updated_at) FROM stdin;
\.


--
-- Data for Name: aa_kb_document; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.aa_kb_document (id, doc_id, title, summary, file_name, file_type, file_size, file_hash, content_text, char_count, chunk_count, embed_status, embed_error, source, storage_path, dir_id, visibility, created_by, created_at, updated_at) FROM stdin;
\.


--
-- Data for Name: aa_kb_grant; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.aa_kb_grant (id, target_type, target_id, action, user_ids, dept_id, role_ids, post_ids, exclude, group_id, set_id, created_by, created_at) FROM stdin;
\.


--
-- Name: aa_kb_access_log_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.aa_kb_access_log_id_seq', 1, false);


--
-- Name: aa_kb_chunk_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.aa_kb_chunk_id_seq', 1, false);


--
-- Name: aa_kb_dir_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.aa_kb_dir_id_seq', 1, false);


--
-- Name: aa_kb_document_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.aa_kb_document_id_seq', 1, false);


--
-- Name: aa_kb_grant_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.aa_kb_grant_id_seq', 1, false);


--
-- Name: aa_kb_access_log aa_kb_access_log_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.aa_kb_access_log
    ADD CONSTRAINT aa_kb_access_log_pkey PRIMARY KEY (id);


--
-- Name: aa_kb_chunk aa_kb_chunk_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.aa_kb_chunk
    ADD CONSTRAINT aa_kb_chunk_pkey PRIMARY KEY (id);


--
-- Name: aa_kb_dir aa_kb_dir_dir_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.aa_kb_dir
    ADD CONSTRAINT aa_kb_dir_dir_id_key UNIQUE (dir_id);


--
-- Name: aa_kb_dir aa_kb_dir_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.aa_kb_dir
    ADD CONSTRAINT aa_kb_dir_pkey PRIMARY KEY (id);


--
-- Name: aa_kb_document aa_kb_document_doc_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.aa_kb_document
    ADD CONSTRAINT aa_kb_document_doc_id_key UNIQUE (doc_id);


--
-- Name: aa_kb_document aa_kb_document_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.aa_kb_document
    ADD CONSTRAINT aa_kb_document_pkey PRIMARY KEY (id);


--
-- Name: aa_kb_grant aa_kb_grant_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.aa_kb_grant
    ADD CONSTRAINT aa_kb_grant_pkey PRIMARY KEY (id);


--
-- Name: idx_aa_kb_access_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_aa_kb_access_created ON public.aa_kb_access_log USING btree (created_at DESC);


--
-- Name: idx_aa_kb_access_doc; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_aa_kb_access_doc ON public.aa_kb_access_log USING btree (doc_id, created_at DESC);


--
-- Name: idx_aa_kb_chunk_doc; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_aa_kb_chunk_doc ON public.aa_kb_chunk USING btree (doc_id, seq);


--
-- Name: idx_aa_kb_dir_parent; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_aa_kb_dir_parent ON public.aa_kb_dir USING btree (parent_id);


--
-- Name: idx_aa_kb_document_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_aa_kb_document_created ON public.aa_kb_document USING btree (created_at DESC);


--
-- Name: idx_aa_kb_document_dir; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_aa_kb_document_dir ON public.aa_kb_document USING btree (dir_id);


--
-- Name: idx_aa_kb_document_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_aa_kb_document_hash ON public.aa_kb_document USING btree (file_hash);


--
-- Name: idx_aa_kb_document_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_aa_kb_document_type ON public.aa_kb_document USING btree (file_type);


--
-- Name: idx_aa_kb_grant_target; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_aa_kb_grant_target ON public.aa_kb_grant USING btree (target_type, target_id, action);


--
-- Name: uq_aa_kb_dir_name; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uq_aa_kb_dir_name ON public.aa_kb_dir USING btree (parent_id, name);


--
-- PostgreSQL database dump complete
--

\unrestrict hEtrrYxAHnSPGJX4omlSw8pCdufI6YMok3FJkg65kE3IRmpFImgYxUcYXwqMfLh

